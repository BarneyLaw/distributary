package fanout

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/gorilla/websocket"
)

type client struct {
	hub      *hub
	conn     *websocket.Conn
	sentSeq  uint64
	ackedSeq uint64

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
	defer func() {
		c.close()
		wg.Done()
	}()

	snapshot := c.hub.getSnapshot()

	for {
		// All errors will cause the writer to exit.
		select {
		case <-snapshot.notification:
			// Do nothing, proceed
		case <-ctx.Done():
			return // Reader exit, writer must exit too
		case <-c.hub.shutdown.Done():
			return // Hub shutdown, all clients shall terminate
		}

		snapshot = c.hub.getSnapshot()

		caption, error := json.Marshal(snapshot.caption)

		if error != nil {
			return
		}

		writeTimeout := 10 * time.Second
		errDeadline := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if errDeadline != nil {
			return
		}

		err := c.conn.WriteMessage(websocket.TextMessage, caption)

		if err != nil {
			return
		}

		c.mu.Lock()
		c.sentSeq++
		isExceedingWindow := c.sentSeq-c.ackedSeq >= 8
		cpSentSeq := c.sentSeq
		c.mu.Unlock()

		if isExceedingWindow && c.ping(cpSentSeq) != nil {
			return
		}

	}

}

func (c *client) ping(num uint64) error {
	err := c.conn.WriteControl(websocket.PingMessage, protocol.EncodeAck(num), time.Now().Add(c.hub.controlTimeout))
	return err
}

func (c *client) keepalive() (stop func()) {
	done := make(chan struct{})

	go func() {
		t := time.NewTicker(c.hub.pingInterval)
		defer t.Stop()

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
