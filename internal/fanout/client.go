package fanout

import (
	"context"
	"errors"
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

	acked chan struct{}

	mu        sync.Mutex
	closeOnce sync.Once
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
	keepAliveCtx, keepAliveCancel := context.WithCancel(context.Background())
	stop := c.keepalive(keepAliveCancel)

	defer func() {
		stop()
		c.close()
		wg.Done()
	}()

	history, cursor := c.hub.sess.Join(c.hello)

	if errHistory := c.write(history, ctx, keepAliveCtx); errHistory != nil {
		return
	}

	for {
		frames, wake, behind := c.hub.sess.Pick(&cursor, 1, true)

		if behind {
			return
		}

		if len(frames) != 0 {
			err := c.write(frames[0], ctx, keepAliveCtx)

			if err != nil {
				return
			}
		}

		// All errors will cause the writer to exit.
		select {
		case <-wake:
		// Do nothing, proceed
		case <-ctx.Done():
			return // Reader exit, writer must exit too
		case <-keepAliveCtx.Done():
			return // Keep alive ping fails, writer must exit
		case <-c.hub.shutdown.Done():
			return // Hub shutdown, all clients shall terminate
		}

	}

}

func (c *client) write(caption []byte, writerCtx context.Context, keepAliveCtx context.Context) error {
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
	isExceedingWindow := c.sentSeq-c.ackedSeq >= 8
	cpSentSeq := c.sentSeq
	acked := c.acked
	c.mu.Unlock()

	if isExceedingWindow {
		return c.startPingingMode(cpSentSeq, writerCtx, keepAliveCtx, acked)
	}
	return nil
}

func (c *client) startPingingMode(num uint64, writerCtx context.Context, keepAliveCtx context.Context, acked chan struct{}) error {
	t := time.NewTimer(5 * time.Second)
	deadTimer := time.NewTimer(c.hub.pingInterval)
	defer func() {
		t.Stop()
		deadTimer.Stop()
	}()
	for {
		if err := c.ping(num); err != nil {
			return err
		}
		select {
		case <-acked:
			return nil
		case <-writerCtx.Done():
			return nil // Reader exit, writer must exit too
		case <-keepAliveCtx.Done():
			return nil // Keep alive ping fails, writer must exit
		case <-c.hub.shutdown.Done():
			return nil // Hub shutdown, all clients shall terminate
		case <-t.C:
			t.Reset(5 * time.Second) // Continue
		case <-deadTimer.C:
			return errors.New("Client is dead") // Ack does not advance for > 20 seconds after entering pinging mode -> client is considered dead
		}
	}
}

func (c *client) ping(num uint64) error {
	err := c.conn.WriteControl(websocket.PingMessage, protocol.EncodeAck(num), time.Now().Add(c.hub.controlTimeout))
	return err
}

func (c *client) keepalive(ctxCancel context.CancelFunc) (stop func()) {
	done := make(chan struct{})

	go func() {
		t := time.NewTicker(c.hub.pingInterval)
		defer func() {
			t.Stop()
			ctxCancel()
		}()

		for {
			select {
			case <-t.C:
				if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.hub.controlTimeout)); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	return func() { close(done) }
}
