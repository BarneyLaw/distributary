// Package server is the HTTP entry point. Every connection to the server's
// one port arrives here first. The router upgrades it to a WebSocket, reads
// the first message, and hands the connection to whoever owns that kind of
// peer:
//
//	SOURCE_HELLO -> Sources.Serve   (the caption source handler)
//	CLIENT_HELLO -> Clients.AddClient (fanout)
//	anything else, or nothing within HelloTimeout -> close with 1008
//
// The router never reads past the hello and never writes captions. Once it
// has dispatched a connection, the handler owns it until it returns; the
// router then closes the connection.
package server

import (
	"log"
	"net/http"
	"time"
 
	"github.com/gorilla/websocket"
 
	"github.com/BarneyLaw/distributary/internal/protocol"

)

// Defaults
const (
	DefaultHelloTimeout = 5 * time.Second
	DefaultMaxMessageSize = 1 << 20 // 1 MiB
	controlTimeout = 5 * time.Second
)

// Close codes sent by the router
const (
	closePolicyViolation = 1008 // bad or missing hello
	closeGoingAway       = 1001 // client handles is shutting down, or source is done
)

// SourceHandler runs one caption source connection until it ends.
// internal/source implements this interface.
type SourceHandler interface {
	Handle(ws *websocket.Conn, hello *protocol.SourceHello)
}

// ClientHandler runs one student connection until it ends, and returns an error 
// without serving if it can no longer accept clients (for example during shutdowns).
// internal/fanout implements this interface.
type ClientHandler interface {
	AddClient(ws *websocket.Conn, hello *protocol.ClientHello) error
}

// Router implements http.Handler. Source, Client and Log are required.
type Router struct {
	Sources SourceHandler
	Clients ClientHandler
	Log    *log.Logger

	// Static, if set, serves plain (non-WebSocket) HTTP requests, such as the
	// browser client's files. If nil, they get 426 Upgrade Required.
	Static http.Handler

	// HelloTimeout is the maximum time to wait for a hello message. 
	// If zero, a default is used.
	HelloTimeout time.Duration

	// MaxMessageSize is the maximum size of a WebSocket message. 
	// If zero, a default is used.
	MaxMessageSize int64
}

var upgrader = websocket.Upgrader{
	// The browser demo may be served from another port or host, and the
	// starter's websockets server did not check Origin either.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ServeHTTP upgrades the request, reads and decodes the hello, and dispatches
// the connection. It returns when the handler that took the connection returns.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		if rt.Static != nil {
			rt.Static.ServeHTTP(w, r)
			return
		}
		http.Error(w, "This endpoint only accepts WebSocket connections", http.StatusUpgradeRequired)
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	defer ws.Close()

	ws.SetReadLimit(rt.MaxMessageSize)

	hello, err := rt.readHello(ws)
	if err != nil {
		rt.Log.Printf("rejected %s: %v", r.RemoteAddr, err)
		closeWith(ws, closePolicyViolation, "Expected SOURCE_HELLO or CLIENT_HELLO")
		return
	} 

	switch h := hello.(type) {
	case *protocol.SourceHello:
		rt.Sources.Handle(ws, h)
	case *protocol.ClientHello:
		if err := rt.Clients.AddClient(ws, h); err != nil {
			rt.Log.Printf("client %d not added: %v", h.ClientID, err)
			closeWith(ws, closeGoingAway, "Server shutting down")
		}
	}
}


// readHello reads the first message under HelloTimeout, then clears the read
// deadline so each handler can set its own. Pings that arrive while waiting
// are answered by gorilla's default ping handler.
func (rt *Router) readHello(ws *websocket.Conn) (any, error) {
	if err := ws.SetReadDeadline(time.Now().Add(rt.helloTimeout())); err != nil {
		return nil, err
	}
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	hello, err := protocol.DecodeHello(raw)
	if err != nil {
		return nil, err
	}
	if err := ws.SetReadDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return hello, nil
}

func (rt *Router) helloTimeout() time.Duration {
	if rt.HelloTimeout > 0 {
		return rt.HelloTimeout
	}
	return DefaultHelloTimeout
}
 
func (rt *Router) maxMessageSize() int64 {
	if rt.MaxMessageSize > 0 {
		return rt.MaxMessageSize
	}
	return DefaultMaxMessageSize
}

func closeWith(ws *websocket.Conn, code int, reason string) {
	msg := websocket.FormatCloseMessage(code, reason)
	_ = ws.WriteControl(websocket.CloseMessage, msg, time.Now().Add(controlTimeout))
}

