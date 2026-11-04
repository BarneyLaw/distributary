package fanout

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/gorilla/websocket"
)

type client struct {
	hub      *hub
	conn     *websocket.Conn
	hello    *protocol.ClientHello
	sentSeq  uint64
	ackedSeq uint64

	timeSent  time.Time
	timeAcked time.Time
	sampleRTT time.Duration
	sRTT      time.Duration // Smoothed RTT

	minRTT time.Duration

	queueDelay time.Duration

	// Count how many observations queueDelay has been within or beyond the threshold. Reset everytime threshold is crossed.
	// Capped at c.hub.modeThreshold + 1
	thresholdCount uint8

	isDegraded bool

	acked chan struct{}

	mu        sync.Mutex
	closeOnce sync.Once
}

// Must be executed inside mutex lock
// Smoothed RTT
func (c *client) computeSRTT(alpha float64) {
	if c.sRTT == 0 {
		c.sRTT = c.sampleRTT
	} else {
		c.sRTT = time.Duration((1-alpha)*float64(c.sRTT) + alpha*float64(c.sampleRTT))
	}
}

// Must be executed inside a mutex lock
func (c *client) computeMinRTT() {
	if c.sampleRTT < c.minRTT {
		c.minRTT = c.sampleRTT
	}
}

// Must be executed inside a mutex lock
func (c *client) computeQueueDelay() {
	temp := c.sRTT - c.minRTT

	if !c.inThreshold(c.queueDelay, temp, c.hub.queueDelayTreshold) {
		c.thresholdCount = 0
	}

	c.queueDelay = temp
	if c.thresholdCount < c.hub.modeThreshold+1 {
		c.thresholdCount++
	}
}

// Must be executed inside a mutex lock
func (c *client) determineStatus() {
	sampleQueueDelay := c.sampleRTT - c.minRTT // Used for recovery

	if !c.isDegraded && c.thresholdCount == c.hub.modeThreshold {
		c.isDegraded = c.queueDelay > c.hub.queueDelayTreshold
	} else if c.isDegraded && sampleQueueDelay <= c.hub.queueDelayTreshold { // Rapid convergence
		c.isDegraded = false
	}

}

func (c *client) inThreshold(before time.Duration, after time.Duration, threshold time.Duration) bool {
	return (before <= threshold && after <= threshold) || (before > threshold && after > threshold)
}

func (c *client) closeWith(ws *websocket.Conn, code int, reason string) {
	msg := websocket.FormatCloseMessage(code, reason)
	_ = ws.WriteControl(websocket.CloseMessage, msg, time.Now().Add(c.hub.controlTimeout))
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		c.conn.Close()
	})
}

func (c *client) readPump(wg *sync.WaitGroup, ctxCancel context.CancelFunc) {
	defer func() {
		c.close()
		ctxCancel()
		wg.Done()
	}()

	c.conn.SetPongHandler(func(s string) error {
		ack, err := protocol.DecodeAck([]byte(s))

		c.mu.Lock()
		if err == nil && ack > c.ackedSeq && ack <= c.sentSeq {
			c.timeAcked = time.Now()
			c.sampleRTT = c.timeAcked.Sub(c.timeSent)
			c.computeMinRTT()
			c.computeSRTT(0.125)
			c.computeQueueDelay()
			c.determineStatus()
			// A pong this late means the client's library has stopped reading:
			// it holds seconds of stale frames. Reset the connection (RST drops
			// the backlog in kernel buffers); the client reconnects and gets
			// every missed final in one HISTORY message.
			if c.sampleRTT-c.minRTT > c.hub.resetDelay {
				if tcp, ok := c.conn.UnderlyingConn().(*net.TCPConn); ok {
					_ = tcp.SetLinger(0)
				}
				c.conn.Close()
			}
			c.ackedSeq = ack
			wakeWriter := c.acked
			c.acked = make(chan struct{})
			close(wakeWriter)
		}
		c.mu.Unlock()

		return c.conn.SetReadDeadline(time.Now().Add(c.hub.pingInterval + c.hub.pongTimeout))
	})

	for {
		errDeadline := c.conn.SetReadDeadline(time.Now().Add(c.hub.pingInterval + c.hub.pongTimeout))
		if errDeadline != nil {
			return
		}

		_, _, err := c.conn.ReadMessage()

		if err != nil {
			break
		}
	}
}

func (c *client) writePump(wg *sync.WaitGroup, ctx context.Context) {
	t := time.NewTicker(c.hub.pingInterval) // keep alive timer

	defer func() {
		t.Stop()
		c.close()
		wg.Done()
	}()

	history, cursor := c.hub.sess.Join(c.hello)

	if errHistory := c.write(history, ctx); errHistory != nil {
		return
	}

	for {
		c.mu.Lock()
		requireProvisional := !c.isDegraded
		c.mu.Unlock()
		frames, wake, behind := c.hub.sess.Pick(&cursor, 1, requireProvisional)

		if behind {
			c.closeWith(c.conn, websocket.CloseTryAgainLater, "Client fell behind retained history")
			return
		}

		if len(frames) != 0 {
			err := c.write(frames[0], ctx)

			if err != nil {
				return
			}
			continue // More source may be pending, wait only if Pick has nothing
		}

		// All errors will cause the writer to exit.
		select {
		case <-wake:
		// Do nothing, proceed
		case <-ctx.Done():
			return // Reader exit, writer must exit too
		case <-t.C:
			// trigger keepalive ping
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.hub.controlTimeout)); err != nil {
				return // Exit if keepalive ping fails
			}
		case <-c.hub.shutdown.Done():
			return // Hub shutdown, all clients shall terminate
		}

	}

}

func (c *client) write(caption []byte, writerCtx context.Context) error {
	writeTimeout := 10 * time.Second
	errDeadline := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if errDeadline != nil {
		return errDeadline
	}

	err := c.conn.WriteMessage(websocket.TextMessage, caption)

	if err != nil {
		return err
	}

	c.mu.Lock()
	c.sentSeq++
	isExceedingWindow := c.sentSeq-c.ackedSeq >= c.hub.maxInFlight
	cpSentSeq := c.sentSeq
	acked := c.acked
	isDegraded := c.isDegraded
	c.mu.Unlock()

	// When client is degraded, every sequence shall trigger ack to re-evaluate the status. This supports rapid-convergence.
	if isExceedingWindow || isDegraded {
		return c.startPingingMode(cpSentSeq, writerCtx, acked)
	}
	return nil
}

func (c *client) startPingingMode(num uint64, writerCtx context.Context, acked chan struct{}) error {
	deadTimer := time.NewTimer(c.hub.pongTimeout)
	defer func() {
		deadTimer.Stop()
	}()
	c.mu.Lock()
	c.timeSent = time.Now()
	c.mu.Unlock()
	for {
		if err := c.ping(num); err != nil { // Send once and wait. This ping will not be accumulated as no keepalive ping comes after this
			return err
		}
		select {
		case <-acked:
			return nil
		case <-writerCtx.Done():
			return nil // Reader exit, writer must exit too
		case <-c.hub.shutdown.Done():
			return nil // Hub shutdown, all clients shall terminate
		case <-deadTimer.C:
			return errors.New("Client is dead") // Ack does not advance for > 20 seconds after entering pinging mode -> client is considered dead
		}
	}
}

func (c *client) ping(num uint64) error {
	err := c.conn.WriteControl(websocket.PingMessage, protocol.EncodeAck(num), time.Now().Add(c.hub.controlTimeout))
	return err
}
