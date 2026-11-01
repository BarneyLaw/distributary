package fanout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/BarneyLaw/distributary/internal/session"
	"github.com/gorilla/websocket"
)

// This test is not independent from client since hub functionalities depends heavily on client.

const testTimeout = 2 * time.Second

// Implemented with Codex
// websocketPair provides a real, in-memory client/server WebSocket pair. Hub
// accepts the concrete *websocket.Conn type, so this is the smallest useful
// stub without changing the production API merely to accommodate tests.
func websocketPair(t *testing.T) (serverConn, clientConn *websocket.Conn) {
	t.Helper()

	type upgradeResult struct {
		conn *websocket.Conn
		err  error
	}

	upgraded := make(chan upgradeResult, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		}).Upgrade(w, r, nil)
		upgraded <- upgradeResult{conn: conn, err: err}
	}))
	t.Cleanup(server.Close)

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial test WebSocket: %v", err)
	}

	select {
	case result := <-upgraded:
		if result.err != nil {
			clientConn.Close()
			t.Fatalf("upgrade test WebSocket: %v", result.err)
		}
		serverConn = result.conn
	case <-time.After(testTimeout):
		clientConn.Close()
		t.Fatal("timed out waiting for test WebSocket upgrade")
	}

	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})
	return serverConn, clientConn
}

// Implemented with Codex
func newTestSession() *session.Session {
	return session.New(session.Config{})
}

// Implemented with Codex
func newTestClientHello() *protocol.ClientHello {
	return &protocol.ClientHello{
		Type:           protocol.TypeClientHello,
		ClientID:       1,
		Session:        "test-session",
		HistorySeconds: 30,
	}
}

// Implemented with Codex
func addClientAsync(h *hub, conn *websocket.Conn, hello *protocol.ClientHello) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- h.AddClient(conn, hello)
	}()
	return done
}

// Implemented with Codex
func requireResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for operation to finish")
		return nil
	}
}

func TestNewHubInitializesUsableDefaults(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because zero or missing timing, size, context, or
	// synchronization fields would make a freshly-created hub unsafe to use.
	sess := newTestSession()
	h := NewHub(sess)
	t.Cleanup(h.Shutdown)

	if h.sess != sess {
		t.Fatal("NewHub did not retain the supplied session")
	}
	if h.clients == nil {
		t.Fatal("NewHub did not initialize the client wait group")
	}
	if h.maxMessageSize != 1<<20 {
		t.Fatalf("maxMessageSize = %d, want %d", h.maxMessageSize, 1<<20)
	}
	if h.maxInFlight != 4 {
		t.Fatalf("maxMessageSize = %d, want %d", h.maxInFlight, 4)
	}
	if h.pingInterval != 20*time.Second {
		t.Fatalf("pingInterval = %v, want %v", h.pingInterval, 20*time.Second)
	}
	if h.pongTimeout != 20*time.Second {
		t.Fatalf("pongTimeout = %v, want %v", h.pongTimeout, 20*time.Second)
	}
	if h.controlTimeout != 5*time.Second {
		t.Fatalf("controlTimeout = %v, want %v", h.controlTimeout, 5*time.Second)
	}
	if h.queueDelayTreshold != 30*time.Millisecond {
		t.Fatalf("queueDelayTreshold = %v, want %v", h.queueDelayTreshold, 30*time.Millisecond)
	}
	if h.modeThreshold != 2 {
		t.Fatalf("modeThreshold = %v, want %v", h.modeThreshold, 2)
	}
	if h.shutdown == nil || h.shutdownFunc == nil {
		t.Fatal("NewHub did not initialize shutdown state")
	}
	if err := h.shutdown.Err(); err != nil {
		t.Fatalf("new hub is already shut down: %v", err)
	}
}

func TestAddClientRejectsClientAfterShutdown(t *testing.T) {
	// Implemented with Codex
	// This test is necessary to enforce the lifecycle boundary and, by passing
	// nil, prove rejection happens before the connection is accessed.
	h := NewHub(newTestSession())
	h.Shutdown()

	err := h.AddClient(nil, newTestClientHello())
	if err == nil {
		t.Fatal("AddClient succeeded after Shutdown")
	}
	const want = "Hub has been closed. No client addition is allowed"
	if err.Error() != want {
		t.Fatalf("AddClient error = %q, want %q", err, want)
	}
}

func TestAddClientAppliesReadLimit(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because an unapplied read limit lets one client
	// force unbounded message allocation. Exceeding it must end that client.
	h := NewHub(newTestSession())
	h.maxMessageSize = 4
	h.pingInterval = time.Hour
	t.Cleanup(h.Shutdown)

	serverConn, clientConn := websocketPair(t)
	addResult := addClientAsync(h, serverConn, newTestClientHello())

	if err := clientConn.WriteMessage(websocket.TextMessage, []byte("12345")); err != nil {
		t.Fatalf("write oversized message testcase error: %v", err)
	}
	if err := requireResult(t, addResult); err != nil {
		t.Fatalf("AddClient returned an unexpected error: %v", err)
	}
}

func TestAddClientReturnsWhenPeerDisconnects(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because AddClient owns two goroutines and must not
	// leak either one after the remote peer goes away.
	h := NewHub(newTestSession())
	h.pingInterval = time.Hour
	t.Cleanup(h.Shutdown)

	serverConn, clientConn := websocketPair(t)
	addResult := addClientAsync(h, serverConn, newTestClientHello())

	if err := clientConn.Close(); err != nil {
		t.Fatalf("close peer connection: %v", err)
	}
	if err := requireResult(t, addResult); err != nil {
		t.Fatalf("AddClient returned an unexpected error: %v", err)
	}
}

func TestShutdownTerminatesActiveClient(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because shutdown must cancel the writer, close the
	// socket, unblock the reader, and wait for the complete client lifecycle.
	h := NewHub(newTestSession())
	h.pingInterval = 10 * time.Millisecond
	h.pongTimeout = time.Second
	h.controlTimeout = time.Second

	serverConn, clientConn := websocketPair(t)
	addResult := addClientAsync(h, serverConn, newTestClientHello())

	// Receiving a ping proves AddClient has registered the client and started
	// its workers before Shutdown is invoked, avoiding a scheduler-dependent
	// race between the two calls.
	pingSeen := make(chan struct{}, 1)
	clientConn.SetPingHandler(func(data string) error {
		select {
		case pingSeen <- struct{}{}:
		default:
		}
		return clientConn.WriteControl(
			websocket.PongMessage,
			[]byte(data),
			time.Now().Add(time.Second),
		)
	})
	peerReadDone := make(chan error, 1)
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				peerReadDone <- err
				return
			}
		}
	}()

	select {
	case <-pingSeen:
	case <-time.After(testTimeout):
		t.Fatal("active client did not receive a keepalive ping")
	}

	shutdownDone := make(chan struct{})
	go func() {
		h.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
	case <-time.After(testTimeout):
		t.Fatal("Shutdown did not finish with an active client")
	}
	if err := requireResult(t, addResult); err != nil {
		t.Fatalf("AddClient returned an unexpected error: %v", err)
	}
	select {
	case err := <-peerReadDone:
		if err == nil {
			t.Fatal("peer read unexpectedly succeeded after hub shutdown")
		}
	case <-time.After(testTimeout):
		t.Fatal("peer connection remained open after hub shutdown")
	}
}

func TestShutdownWaitsForRegisteredClients(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because Shutdown returning while clients are still
	// active would let callers tear down dependencies those clients still use.
	h := NewHub(newTestSession())
	h.clients.Add(1)

	shutdownDone := make(chan struct{})
	go func() {
		h.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-h.shutdown.Done():
		// Cancellation occurs immediately before waiting for clients.
	case <-time.After(testTimeout):
		t.Fatal("Shutdown did not cancel its context")
	}
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned before the registered client completed")
	default:
	}

	h.clients.Done()
	select {
	case <-shutdownDone:
	case <-time.After(testTimeout):
		t.Fatal("Shutdown did not return after the registered client completed")
	}
}

func TestShutdownIsIdempotentUnderConcurrency(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because production shutdown signals often arrive
	// concurrently; the cancellation side effect must execute exactly once.
	ctx, cancel := context.WithCancel(context.Background())
	var cancelCalls atomic.Int32
	h := &hub{
		clients:  &sync.WaitGroup{},
		shutdown: ctx,
		shutdownFunc: func() {
			cancelCalls.Add(1)
			cancel()
		},
	}

	const callers = 32
	var callersDone sync.WaitGroup
	callersDone.Add(callers)
	for range callers {
		go func() {
			defer callersDone.Done()
			h.Shutdown()
		}()
	}

	finished := make(chan struct{})
	go func() {
		callersDone.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(testTimeout):
		t.Fatal("concurrent Shutdown calls did not all return")
	}

	if got := cancelCalls.Load(); got != 1 {
		t.Fatalf("shutdown cancellation called %d times, want 1", got)
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("shutdown context error = %v, want %v", err, context.Canceled)
	}
}
