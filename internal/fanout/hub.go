package fanout

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/BarneyLaw/distributary/internal/session"
	"github.com/gorilla/websocket"
)

// Hub is the sole gateway for other components to interact with fanout.
type Hub interface {
	AddClient(*websocket.Conn, *protocol.ClientHello) error
	Shutdown()
}

// Keep hub unexported to prevent instantiation outside NewHub().
type hub struct {
	sess *session.Session

	clients *sync.WaitGroup

	maxMessageSize int
	pingInterval   time.Duration
	pongTimeout    time.Duration
	controlTimeout time.Duration

	shutdown     context.Context
	shutdownFunc context.CancelFunc

	mu           sync.Mutex
	shutdownOnce sync.Once
}

// Instantiates a Hub that will orchestrate all clients connection.
// A nil session will be accepted and cause error later on.
// Do not pass a nil session.
func NewHub(session *session.Session) *hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &hub{
		sess:           session,
		clients:        &sync.WaitGroup{},
		maxMessageSize: 1 << 20, // 1 MiB
		pingInterval:   20 * time.Second,
		pongTimeout:    20 * time.Second,
		controlTimeout: 5 * time.Second,
		shutdown:       ctx,
		shutdownFunc:   cancel,
	}
}

// Establishes one-connection-per-client.
// AddClient is a blocking function. Call it using go routine
func (h *hub) AddClient(connection *websocket.Conn, hello *protocol.ClientHello) error {
	h.mu.Lock()
	if h.shutdown.Err() != nil {
		h.mu.Unlock()
		return errors.New("Hub has been closed. No client addition is allowed")
	}

	connection.SetReadLimit(int64(h.maxMessageSize))

	c := &client{
		hub:      h,
		conn:     connection,
		hello:    hello,
		sentSeq:  0,
		ackedSeq: 0,
		acked:    make(chan struct{}),
	}

	// Spawn threads for client
	h.clients.Add(1)
	h.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	wg := &sync.WaitGroup{}

	wg.Add(1)
	go c.readPump(wg, cancel)

	wg.Add(1)
	go c.writePump(wg, ctx)

	wg.Wait()
	h.clients.Done()
	return nil
}

func (h *hub) helperShutdown() {
	h.mu.Lock()
	h.shutdownFunc()
	h.mu.Unlock()
	h.clients.Wait()
}

// Shutdowns a hub once and only once.
func (h *hub) Shutdown() {
	h.shutdownOnce.Do(h.helperShutdown)
}
