// Package naive is a Go port of the professor's starter server
// (harness/starter_server.py). It is the "before" in the report's
// before/after comparison, so it deliberately keeps the starter's weaknesses:
//
//   - every CAPTION_UPDATE is forwarded to every client, and the source
//     handler waits until all of those sends finish before reading the next
//     update, so one stalled client delays everyone;
//   - every CLIENT_HELLO gets an empty HISTORY;
//   - there is no revision state, deduplication, history, per-client
//     isolation, backpressure or bandwidth handling.
//
// Differences from the Python starter, kept small on purpose:
//
//   - captions that fail protocol.DecodeSourceMessage are dropped, where the
//     starter forwarded any JSON whose type was CAPTION_UPDATE;
//   - permessage-deflate compression is not negotiated. The starter's
//     websockets library enables it by default, so under a bandwidth limit
//     this port sends somewhat more bytes than the starter did.
package naive

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/BarneyLaw/distributary/internal/protocol"
)

const (
	maxMessageSize = 1 << 20 // 1 MiB
	helloTimeout   = 5 * time.Second
	pingInterval   = 20 * time.Second
	pongTimeout    = 20 * time.Second
	controlTimeout = 5 * time.Second
)

const closePolicyViolation = 1008

var upgrader = websocket.Upgrader{
	// The browser demo serves client/ from a different port, and the
	// starter's websockets server did not check Origin either.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Server forwards captions from the source to every connected client.
// It implements http.Handler: every request is upgraded to a WebSocket.
type Server struct {
	log 	*log.Logger
	mu 		sync.Mutex
	clients map[*websocket.Conn]struct{}
}	

// New returns a new Server that logs to logger.
func New(logger *log.Logger) *Server {
	return &Server{
		log:     logger,
		clients: make(map[*websocket.Conn]struct{}),
	}
}

// ServeHTTP upgrades the request, reads the hello message and hands the
// connection to the source or client handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Printf("upgrade: %v", err)
		return
	}

	defer ws.Close()
	ws.SetReadLimit(maxMessageSize)

	stopKeepAlive := keepalive(ws)
	defer stopKeepAlive()

	ws.SetReadDeadline(time.Now().Add(helloTimeout))
	_, raw, err := ws.ReadMessage()

	if err != nil {
		closeWith(ws, "Expected valid hello message")
		return
	}

	hello, err := protocol.DecodeHello(raw)
	if err != nil {
		closeWith(ws, "Expected SOURCE_HELLO or CLIENT_HELLO")
		return
	}
	extendReadDeadline(ws)

	switch h := hello.(type) {
	case *protocol.SourceHello:
		s.handleSource(ws, h)
	case *protocol.ClientHello:
		s.handleClient(ws, h)
	}
}

// handleSource reads caption updates and broadcasts each one before reading
// the next, exactly like the starter's `await broadcast_naively(raw)`.
func (s *Server) handleSource(ws *websocket.Conn, hello *protocol.SourceHello) {
	s.log.Printf("source connected: %s", hello.Session)
	defer s.log.Printf("source disconnected: %s", hello.Session)

	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		extendReadDeadline(ws)

		msg, err := protocol.DecodeSourceMessage(raw)
		if err != nil {
			continue
		}
		switch msg.(type) {
		case *protocol.CaptionUpdate:
			// forward exactly the bytes received, as the starter did
			s.broadcast(raw)
		
		case *protocol.SourceDone:
			return
		}
	}
}

// handleClient sends an empty HISTORY, registers the client for broadcasts,
// then reads until the client goes away. Reading also lets the WebSocket
// library answer the client's keepalive pings.
func (server *Server) handleClient(ws *websocket.Conn, hello *protocol.ClientHello) {
	if err := ws.WriteMessage(websocket.TextMessage, protocol.EncodeHistory(nil)); err != nil {
		return
	}

	m := server.add(ws)
	server.log.Printf("client connected: %s (%d clients)", hello.ClientID, m)
	defer func() {
		m := server.remove(ws)
		server.log.Printf("client disconnected: %s (%d clients)", hello.ClientID, m)
	}()

	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		extendReadDeadline(ws)
	}

}

// broadcast sends frame to every client at once and returns when every send
// has finished, matching the starter's asyncio.gather. Clients whose send
// fails are removed. Only the source goroutine calls it, so each connection
// has at most one data write in progress, as gorilla/websocket requires.
func (server *Server) broadcast(frame []byte) {
	snapshot := server.snapshot()
	if len(snapshot) == 0 {
		return // no clients, dont bother
	}

	errs := make([]error, len(snapshot))

	var wg sync.WaitGroup
	for i, c := range snapshot {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.WriteMessage(websocket.TextMessage, frame)
		} ()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			server.remove(snapshot[i])
		}
	}
}


// add adds the connection to the server's client set and returns the new size
// of the set. The caller must not hold the server's mutex while calling this
// method.
func (server *Server) add(c *websocket.Conn) int {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.clients[c] = struct{}{}
	return len(server.clients)
}

// remove removes the connection from the server's client set and returns the
// new size of the set. The caller must not hold the server's mutex while
// calling this method.
func (server *Server) remove(c *websocket.Conn) int {
	server.mu.Lock()
	defer server.mu.Unlock()
	delete(server.clients, c)
	return len(server.clients)
}

// snapshot returns a slice of all currently connected clients. The caller must
// not modify the slice or the connections in it, and must not hold the server's
// mutex while using the slice.
func (server *Server) snapshot() []*websocket.Conn {
	server.mu.Lock()
	defer server.mu.Unlock()
	out := make([]*websocket.Conn, 0, len(server.clients))

	for c := range server.clients {
		out = append(out, c)
	}
	return out
}

// keepalive pings the peer every pingInterval, like the starter's
// ping_interval. Each pong pushes the read deadline forward; if no pong (or
// other message) arrives within pingInterval+pongTimeout, the blocked read
// fails and the handler closes the connection. The returned func stops it.
func keepalive(ws *websocket.Conn) (stop func()) {
	ws.SetPongHandler(func(string) error {
		extendReadDeadline(ws)
		return nil
	})
	done := make(chan struct{})

	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()

		for {
			select {
			case <-t.C:
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(controlTimeout)); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	return func() { close(done) }
}

func extendReadDeadline(ws *websocket.Conn) {
	ws.SetWriteDeadline(time.Now().Add(pingInterval + pongTimeout))
}

func closeWith(ws *websocket.Conn, reason string) {
	msg := websocket.FormatCloseMessage(closePolicyViolation, reason)
	ws.WriteControl(websocket.CloseMessage, msg, time.Now().Add(controlTimeout))
}
