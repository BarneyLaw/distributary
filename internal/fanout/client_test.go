package fanout

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/gorilla/websocket"
)

// This test is not independent from hub since the real hub is created through NewHub().

const clientTestTimeout = 2 * time.Second

// Implemented with Codex
type closeCountingConn struct {
	net.Conn
	closeCalls *atomic.Int32
}

// Implemented with Codex
func (c *closeCountingConn) Close() error {
	c.closeCalls.Add(1)
	return c.Conn.Close()
}

// Implemented with Codex
type closeCountingListener struct {
	net.Listener
	closeCalls *atomic.Int32
}

// Implemented with Codex
func (l *closeCountingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &closeCountingConn{Conn: conn, closeCalls: l.closeCalls}, nil
}

// Implemented with Codex
// clientWebsocketPair is local to the client tests so they do not depend on
// setup helpers, state, or execution order from hub_test.go.
func clientWebsocketPair(t *testing.T) (
	serverConn, peerConn *websocket.Conn,
	serverCloseCalls *atomic.Int32,
) {
	t.Helper()

	type upgradeResult struct {
		conn *websocket.Conn
		err  error
	}

	upgraded := make(chan upgradeResult, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		}).Upgrade(w, r, nil)
		upgraded <- upgradeResult{conn: conn, err: err}
	}))
	serverCloseCalls = &atomic.Int32{}
	server.Listener = &closeCountingListener{
		Listener:   server.Listener,
		closeCalls: serverCloseCalls,
	}
	server.Start()
	t.Cleanup(server.Close)

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	peerConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial client-test WebSocket: %v", err)
	}

	select {
	case result := <-upgraded:
		if result.err != nil {
			peerConn.Close()
			t.Fatalf("upgrade client-test WebSocket: %v", result.err)
		}
		serverConn = result.conn
	case <-time.After(clientTestTimeout):
		peerConn.Close()
		t.Fatal("timed out waiting for client-test WebSocket upgrade")
	}

	t.Cleanup(func() {
		peerConn.Close()
		serverConn.Close()
	})
	return serverConn, peerConn, serverCloseCalls
}

// Implemented with Codex
// clientFixture deliberately constructs only the concrete dependencies client
// needs. client stores *hub and *websocket.Conn rather than interfaces, so a
// minimal hub plus an in-memory WebSocket peer is the closest useful stub.
type clientFixture struct {
	client              *client
	hub                 *hub
	peer                *websocket.Conn
	transportCloseCalls *atomic.Int32
}

// Implemented with Codex
func newClientFixture(t *testing.T) clientFixture {
	t.Helper()

	serverConn, peerConn, transportCloseCalls := clientWebsocketPair(t)
	h := NewHub(CreateSession())
	h.pingInterval = time.Hour
	h.pongTimeout = time.Second
	h.controlTimeout = time.Second

	c := &client{hub: h, conn: serverConn, acked: make(chan struct{})}
	t.Cleanup(func() {
		c.close()
		h.Shutdown()
	})

	return clientFixture{
		client:              c,
		hub:                 h,
		peer:                peerConn,
		transportCloseCalls: transportCloseCalls,
	}
}

// Implemented with Codex
func waitForWaitGroup(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(clientTestTimeout):
		t.Fatal("timed out waiting for client pump to stop")
	}
}

// Implemented with Codex
// waitForWritePumpStart gives writePump time to take its initial snapshot and
// block on that snapshot's notification channel. There is no externally
// observable ready signal in the production API; keeping this synchronization
// in one helper makes that limitation explicit and avoids racing publication
// against the initial getSnapshot call.
func waitForWritePumpStart() {
	time.Sleep(10 * time.Millisecond)
}

// Implemented with Codex
func requireSentSequence(t *testing.T, c *client, want uint64) {
	t.Helper()

	deadline := time.Now().Add(clientTestTimeout)
	for {
		c.mu.Lock()
		got := c.sentSeq
		c.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sentSeq = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// Implemented with Codex
func publishTestCaption(s *Session, caption *protocol.SourceHello) {
	s.mu.Lock()
	s.caption = caption
	close(s.notification)
	s.notification = make(chan struct{})
	s.mu.Unlock()
}

// Implemented with Codex
type websocketRead struct {
	messageType int
	payload     []byte
	err         error
}

// Implemented with Codex
func readWebsocketAsync(conn *websocket.Conn) <-chan websocketRead {
	result := make(chan websocketRead, 1)
	go func() {
		messageType, payload, err := conn.ReadMessage()
		result <- websocketRead{messageType: messageType, payload: payload, err: err}
	}()
	return result
}

// Implemented with Codex
func requireWebsocketRead(t *testing.T, result <-chan websocketRead) websocketRead {
	t.Helper()

	select {
	case got := <-result:
		return got
	case <-time.After(clientTestTimeout):
		t.Fatal("timed out waiting for WebSocket frame")
		return websocketRead{}
	}
}

func TestClientCloseIsIdempotentUnderConcurrency(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because the read pump, write pump, and cleanup path
	// can all close the same connection concurrently. Besides leaving the peer
	// disconnected, those calls must execute the underlying transport close
	// exactly once.
	f := newClientFixture(t)
	if got := f.transportCloseCalls.Load(); got != 0 {
		t.Fatalf("transport Close called %d times before client.close, want 0", got)
	}

	const callers = 32
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			f.client.close()
		}()
	}
	waitForWaitGroup(t, &wg)
	if got := f.transportCloseCalls.Load(); got != 1 {
		t.Fatalf("transport Close called %d times, want exactly 1", got)
	}

	result := requireWebsocketRead(t, readWebsocketAsync(f.peer))
	if result.err == nil {
		t.Fatalf("peer read succeeded after concurrent close: type=%d payload=%q", result.messageType, result.payload)
	}
}

func TestClientPingSendsEncodedSequence(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because flow control depends on the ping payload
	// carrying the exact sent sequence in the protocol's binary ACK format.
	f := newClientFixture(t)

	pingPayload := make(chan []byte, 1)
	f.peer.SetPingHandler(func(payload string) error {
		pingPayload <- []byte(payload)
		return nil
	})
	peerRead := readWebsocketAsync(f.peer)

	const wantSequence = uint64(0x0102030405060708)
	if err := f.client.ping(wantSequence); err != nil {
		t.Fatalf("ping returned an unexpected error: %v", err)
	}

	select {
	case payload := <-pingPayload:
		gotSequence, err := protocol.DecodeAck(payload)
		if err != nil {
			t.Fatalf("ping payload is not a protocol ACK: %v", err)
		}
		if gotSequence != wantSequence {
			t.Fatalf("ping sequence = %d, want %d", gotSequence, wantSequence)
		}
	case <-time.After(clientTestTimeout):
		t.Fatal("peer did not receive ping")
	}

	f.client.close()
	_ = requireWebsocketRead(t, peerRead)
}

func TestClientKeepaliveSendsPingAndStops(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because an idle client has no caption traffic to
	// prove liveness; keepalive must emit a control ping and release its goroutine
	// promptly when the writer asks it to stop.
	f := newClientFixture(t)
	f.hub.pingInterval = 5 * time.Millisecond

	pingSeen := make(chan struct{}, 1)
	f.peer.SetPingHandler(func(string) error {
		pingSeen <- struct{}{}
		return nil
	})
	peerRead := readWebsocketAsync(f.peer)

	keepaliveCtx, cancel := context.WithCancel(context.Background())
	stop := f.client.keepalive(cancel)

	select {
	case <-pingSeen:
	case <-time.After(clientTestTimeout):
		t.Fatal("keepalive did not send a ping")
	}
	stop()

	select {
	case <-keepaliveCtx.Done():
	case <-time.After(clientTestTimeout):
		t.Fatal("keepalive goroutine did not stop")
	}

	f.client.close()
	_ = requireWebsocketRead(t, peerRead)
}

func TestClientKeepaliveCancelsContextWhenPingFails(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because a failed keepalive write is how the writer
	// learns that a silent or broken connection must be abandoned.
	f := newClientFixture(t)
	f.hub.pingInterval = 5 * time.Millisecond
	f.client.close()

	keepaliveCtx, cancel := context.WithCancel(context.Background())
	stop := f.client.keepalive(cancel)
	defer stop()

	select {
	case <-keepaliveCtx.Done():
	case <-time.After(clientTestTimeout):
		t.Fatal("failed keepalive ping did not cancel its context")
	}
}

func TestClientReadPumpAppliesOnlyValidProgressingAcknowledgements(t *testing.T) {
	// Implemented with Codex
	tests := []struct {
		name       string
		why        string
		sentSeq    uint64
		ackedSeq   uint64
		pong       []byte
		wantAckSeq uint64
		wantWake   bool
	}{
		{
			name:       "new acknowledgement advances window",
			why:        "a valid newer ACK must release acknowledged capacity in the flow-control window",
			sentSeq:    5,
			ackedSeq:   1,
			pong:       protocol.EncodeAck(3),
			wantAckSeq: 3,
			wantWake:   true,
		},
		{
			name:       "duplicate acknowledgement is ignored",
			why:        "duplicate pongs are legal network behavior and must not perturb sequence state",
			sentSeq:    5,
			ackedSeq:   3,
			pong:       protocol.EncodeAck(3),
			wantAckSeq: 3,
			wantWake:   false,
		},
		{
			name:       "stale acknowledgement is ignored",
			why:        "an out-of-order old pong must not move the cumulative ACK backwards",
			sentSeq:    5,
			ackedSeq:   3,
			pong:       protocol.EncodeAck(2),
			wantAckSeq: 3,
			wantWake:   false,
		},
		{
			name:       "future acknowledgement is ignored",
			why:        "trusting an ACK beyond sentSeq would let a peer bypass the flow-control window",
			sentSeq:    5,
			ackedSeq:   3,
			pong:       protocol.EncodeAck(6),
			wantAckSeq: 3,
			wantWake:   false,
		},
		{
			name:       "malformed acknowledgement is ignored",
			why:        "ordinary empty keepalive pongs and corrupt payloads must not alter sequence accounting",
			sentSeq:    5,
			ackedSeq:   3,
			pong:       []byte("not-an-ack"),
			wantAckSeq: 3,
			wantWake:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("necessity: %s", tt.why)
			f := newClientFixture(t)
			f.client.sentSeq = tt.sentSeq
			f.client.ackedSeq = tt.ackedSeq
			originalAcked := f.client.acked

			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			wg.Add(1)
			go f.client.readPump(&wg, cancel)

			if err := f.peer.WriteControl(websocket.PongMessage, tt.pong, time.Now().Add(time.Second)); err != nil {
				t.Fatalf("send pong: %v", err)
			}
			// A close frame sent after the pong is an ordering barrier: readPump
			// processes the pong handler before observing the close and exiting.
			if err := f.peer.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second)); err != nil {
				t.Fatalf("send close frame: %v", err)
			}
			waitForWaitGroup(t, &wg)

			if err := ctx.Err(); err != context.Canceled {
				t.Fatalf("readPump context error = %v, want %v", err, context.Canceled)
			}
			f.client.mu.Lock()
			gotAckSeq := f.client.ackedSeq
			f.client.mu.Unlock()
			if gotAckSeq != tt.wantAckSeq {
				t.Fatalf("ackedSeq = %d, want %d", gotAckSeq, tt.wantAckSeq)
			}

			select {
			case <-originalAcked:
				if !tt.wantWake {
					t.Fatal("pong woke the writer without advancing ackedSeq")
				}
			default:
				if tt.wantWake {
					t.Fatal("advancing acknowledgement did not wake the writer")
				}
			}
			f.client.mu.Lock()
			currentAcked := f.client.acked
			f.client.mu.Unlock()
			if tt.wantWake && currentAcked == originalAcked {
				t.Fatal("advancing acknowledgement did not install a fresh notification channel")
			}
			if !tt.wantWake && currentAcked != originalAcked {
				t.Fatal("non-advancing acknowledgement replaced the notification channel")
			}
		})
	}
}

func TestClientReadPumpTimesOutSilentPeer(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because a peer can disappear without sending a
	// close frame; the read deadline must eventually terminate the pump, cancel
	// the writer context, and close the socket.
	f := newClientFixture(t)
	f.hub.pingInterval = 10 * time.Millisecond
	f.hub.pongTimeout = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.readPump(&wg, cancel)
	waitForWaitGroup(t, &wg)

	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("readPump context error = %v, want %v", err, context.Canceled)
	}
	result := requireWebsocketRead(t, readWebsocketAsync(f.peer))
	if result.err == nil {
		t.Fatal("silent peer remained connected after read deadline")
	}
}

func TestClientWritePumpPublishesCaptionAndTracksSequence(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because the writer's primary contract is to wait
	// for notification, serialize the latest snapshot as a text frame, and count
	// exactly one sent sequence for that frame.
	f := newClientFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.writePump(&wg, ctx)
	waitForWritePumpStart()

	wantCaption := &protocol.SourceHello{
		Type:    protocol.TypeSourceHello,
		Session: "client-write-test",
	}
	peerRead := readWebsocketAsync(f.peer)
	publishTestCaption(f.hub.sess, wantCaption)

	got := requireWebsocketRead(t, peerRead)
	if got.err != nil {
		t.Fatalf("read caption frame: %v", got.err)
	}
	if got.messageType != websocket.TextMessage {
		t.Fatalf("caption frame type = %d, want %d", got.messageType, websocket.TextMessage)
	}
	wantPayload, err := json.Marshal(wantCaption)
	if err != nil {
		t.Fatalf("marshal expected caption: %v", err)
	}
	if string(got.payload) != string(wantPayload) {
		t.Fatalf("caption payload = %s, want %s", got.payload, wantPayload)
	}

	// The peer can receive bytes just before WriteMessage returns to the
	// sender, so wait for the immediately-following accounting update.
	requireSentSequence(t, f.client, 1)

	cancel()
	waitForWaitGroup(t, &wg)
}

func TestClientWriteSendsCaptionBelowFlowControlWindow(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because write now owns the data-frame write and
	// sequence accounting independently of writePump. Below the window limit it
	// must return immediately without waiting for an acknowledgement.
	f := newClientFixture(t)
	peerRead := readWebsocketAsync(f.peer)
	writerCtx, cancelWriter := context.WithCancel(context.Background())
	keepAliveCtx, cancelKeepAlive := context.WithCancel(context.Background())
	defer cancelWriter()
	defer cancelKeepAlive()

	wantPayload := []byte(`{"caption":"below-window"}`)
	if err := f.client.write(wantPayload, writerCtx, keepAliveCtx); err != nil {
		t.Fatalf("write returned an unexpected error: %v", err)
	}

	got := requireWebsocketRead(t, peerRead)
	if got.err != nil {
		t.Fatalf("read written caption: %v", got.err)
	}
	if got.messageType != websocket.TextMessage {
		t.Fatalf("message type = %d, want %d", got.messageType, websocket.TextMessage)
	}
	if string(got.payload) != string(wantPayload) {
		t.Fatalf("payload = %q, want %q", got.payload, wantPayload)
	}

	f.client.mu.Lock()
	gotSentSeq := f.client.sentSeq
	gotAckedSeq := f.client.ackedSeq
	f.client.mu.Unlock()
	if gotSentSeq != 1 {
		t.Fatalf("sentSeq = %d, want 1", gotSentSeq)
	}
	if gotAckedSeq != 0 {
		t.Fatalf("ackedSeq = %d, want 0", gotAckedSeq)
	}
}

func TestClientWriteReturnsSocketErrorWithoutAdvancingSequence(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because failed writes must not consume a sequence
	// number or enter flow-control mode for a frame that never reached the peer.
	f := newClientFixture(t)
	f.client.close()

	err := f.client.write([]byte("unwritten"), context.Background(), context.Background())
	if err == nil {
		t.Fatal("write succeeded on a closed connection")
	}
	f.client.mu.Lock()
	gotSentSeq := f.client.sentSeq
	f.client.mu.Unlock()
	if gotSentSeq != 0 {
		t.Fatalf("sentSeq = %d after failed write, want 0", gotSentSeq)
	}
}

func TestClientWriteAtWindowLimitWaitsForReadPumpAcknowledgement(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because write and readPump coordinate through the
	// acked channel: the eighth outstanding frame must ping, wait, then resume
	// only after a valid cumulative pong advances ackedSeq.
	f := newClientFixture(t)
	f.client.sentSeq = 7
	originalAcked := f.client.acked

	pingPayload := make(chan []byte, 1)
	f.peer.SetPingHandler(func(payload string) error {
		pingPayload <- []byte(payload)
		return f.peer.WriteControl(
			websocket.PongMessage,
			[]byte(payload),
			time.Now().Add(time.Second),
		)
	})
	textSeen := make(chan websocketRead, 1)
	peerReadDone := make(chan error, 1)
	go func() {
		for {
			messageType, payload, err := f.peer.ReadMessage()
			if err != nil {
				peerReadDone <- err
				return
			}
			textSeen <- websocketRead{messageType: messageType, payload: payload}
		}
	}()

	readerCtx, cancelReader := context.WithCancel(context.Background())
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go f.client.readPump(&readerWG, cancelReader)

	writerCtx, cancelWriter := context.WithCancel(context.Background())
	keepAliveCtx, cancelKeepAlive := context.WithCancel(context.Background())
	defer cancelWriter()
	defer cancelKeepAlive()
	writeResult := make(chan error, 1)
	wantPayload := []byte(`{"caption":"window-limit"}`)
	go func() {
		writeResult <- f.client.write(wantPayload, writerCtx, keepAliveCtx)
	}()

	select {
	case got := <-textSeen:
		if got.messageType != websocket.TextMessage || string(got.payload) != string(wantPayload) {
			t.Fatalf("written frame = type %d payload %q, want text payload %q", got.messageType, got.payload, wantPayload)
		}
	case <-time.After(clientTestTimeout):
		t.Fatal("peer did not receive the window-limit caption")
	}
	select {
	case payload := <-pingPayload:
		sequence, err := protocol.DecodeAck(payload)
		if err != nil {
			t.Fatalf("decode flow-control ping: %v", err)
		}
		if sequence != 8 {
			t.Fatalf("flow-control ping sequence = %d, want 8", sequence)
		}
	case <-time.After(clientTestTimeout):
		t.Fatal("peer did not receive a flow-control ping")
	}
	select {
	case err := <-writeResult:
		if err != nil {
			t.Fatalf("write returned after acknowledgement with error: %v", err)
		}
	case <-time.After(clientTestTimeout):
		t.Fatal("write did not resume after acknowledgement")
	}

	f.client.mu.Lock()
	gotSentSeq := f.client.sentSeq
	gotAckedSeq := f.client.ackedSeq
	currentAcked := f.client.acked
	f.client.mu.Unlock()
	if gotSentSeq != 8 || gotAckedSeq != 8 {
		t.Fatalf("sequence state = sent %d, acked %d; want sent 8, acked 8", gotSentSeq, gotAckedSeq)
	}
	if currentAcked == originalAcked {
		t.Fatal("readPump did not rotate the acknowledgement notification channel")
	}

	if err := f.peer.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("close peer connection: %v", err)
	}
	waitForWaitGroup(t, &readerWG)
	if err := readerCtx.Err(); err != context.Canceled {
		t.Fatalf("reader context error = %v, want %v", err, context.Canceled)
	}
	select {
	case <-peerReadDone:
	case <-time.After(clientTestTimeout):
		t.Fatal("peer reader did not stop after the client connection closed")
	}
}

func TestClientStartPingingModeStopsOnEachLifecycleSignal(t *testing.T) {
	// Implemented with Codex
	tests := []struct {
		name   string
		why    string
		signal string
	}{
		{
			name:   "acknowledgement",
			why:    "an advancing ACK must release a writer blocked by the flow-control window",
			signal: "ack",
		},
		{
			name:   "reader cancellation",
			why:    "a stopped reader makes further acknowledgement waiting pointless",
			signal: "writer",
		},
		{
			name:   "keepalive cancellation",
			why:    "a failed keepalive means the connection must not remain in pinging mode",
			signal: "keepalive",
		},
		{
			name:   "hub shutdown",
			why:    "global shutdown must release a writer blocked in flow control",
			signal: "hub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("necessity: %s", tt.why)
			f := newClientFixture(t)
			acked := make(chan struct{})
			writerCtx, cancelWriter := context.WithCancel(context.Background())
			keepAliveCtx, cancelKeepAlive := context.WithCancel(context.Background())
			defer cancelWriter()
			defer cancelKeepAlive()

			pingPayload := make(chan []byte, 1)
			f.peer.SetPingHandler(func(payload string) error {
				pingPayload <- []byte(payload)
				return nil
			})
			peerRead := readWebsocketAsync(f.peer)
			result := make(chan error, 1)
			go func() {
				result <- f.client.startPingingMode(17, writerCtx, keepAliveCtx, acked)
			}()

			select {
			case payload := <-pingPayload:
				sequence, err := protocol.DecodeAck(payload)
				if err != nil {
					t.Fatalf("decode pinging-mode payload: %v", err)
				}
				if sequence != 17 {
					t.Fatalf("pinging-mode sequence = %d, want 17", sequence)
				}
			case <-time.After(clientTestTimeout):
				t.Fatal("pinging mode did not send its initial ping")
			}

			switch tt.signal {
			case "ack":
				close(acked)
			case "writer":
				cancelWriter()
			case "keepalive":
				cancelKeepAlive()
			case "hub":
				f.hub.shutdownFunc()
			default:
				t.Fatalf("unknown lifecycle signal %q", tt.signal)
			}

			select {
			case err := <-result:
				if err != nil {
					t.Fatalf("startPingingMode returned an unexpected error: %v", err)
				}
			case <-time.After(clientTestTimeout):
				t.Fatal("startPingingMode did not stop after lifecycle signal")
			}

			f.client.close()
			_ = requireWebsocketRead(t, peerRead)
		})
	}
}

func TestClientStartPingingModeRejectsDeadClient(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because a client that never advances its ACK must
	// eventually release the blocked writer with an explicit dead-client error.
	f := newClientFixture(t)
	f.hub.pingInterval = 20 * time.Millisecond
	peerRead := readWebsocketAsync(f.peer)

	err := f.client.startPingingMode(
		8,
		context.Background(),
		context.Background(),
		make(chan struct{}),
	)
	if err == nil || err.Error() != "Client is dead" {
		t.Fatalf("startPingingMode error = %v, want %q", err, "Client is dead")
	}

	f.client.close()
	_ = requireWebsocketRead(t, peerRead)
}

func TestClientStartPingingModeReturnsPingFailure(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because flow-control recovery cannot proceed when
	// its control ping cannot be written; the socket error must reach writePump.
	f := newClientFixture(t)
	f.client.close()

	err := f.client.startPingingMode(
		8,
		context.Background(),
		context.Background(),
		make(chan struct{}),
	)
	if err == nil {
		t.Fatal("startPingingMode succeeded when its ping could not be written")
	}
}

func TestClientWritePumpStopsOnEachLifecycleSignal(t *testing.T) {
	// Implemented with Codex
	tests := []struct {
		name string
		why  string
		stop func(f clientFixture, cancel context.CancelFunc)
	}{
		{
			name: "reader context cancellation",
			why:  "the writer must not outlive the reader after the peer connection is no longer readable",
			stop: func(_ clientFixture, cancel context.CancelFunc) { cancel() },
		},
		{
			name: "hub shutdown",
			why:  "global shutdown must release an otherwise idle writer waiting for a caption notification",
			stop: func(f clientFixture, _ context.CancelFunc) { f.hub.shutdownFunc() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("necessity: %s", tt.why)
			f := newClientFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var wg sync.WaitGroup
			wg.Add(1)
			go f.client.writePump(&wg, ctx)

			tt.stop(f, cancel)
			waitForWaitGroup(t, &wg)

			result := requireWebsocketRead(t, readWebsocketAsync(f.peer))
			if result.err == nil {
				t.Fatal("peer remained connected after writer stopped")
			}
		})
	}
}

func TestClientWritePumpStopsWhenSocketWriteFails(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because a caption notification can race with peer
	// disconnection; a failed data write must terminate the pump without leaking
	// its keepalive goroutine.
	f := newClientFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.writePump(&wg, ctx)
	waitForWritePumpStart()

	f.client.close()
	publishTestCaption(f.hub.sess, &protocol.SourceHello{
		Type:    protocol.TypeSourceHello,
		Session: "write-failure",
	})
	waitForWaitGroup(t, &wg)
}

func TestClientWritePumpFlowControlWindowBoundary(t *testing.T) {
	// Implemented with Codex
	tests := []struct {
		name        string
		why         string
		ackedSeq    uint64
		wantPing    bool
		wantPingSeq uint64
	}{
		{
			name:        "seven outstanding messages do not ping",
			why:         "the client should use all seven slots below the configured threshold without extra control traffic",
			ackedSeq:    1,
			wantPing:    false,
			wantPingSeq: 0,
		},
		{
			name:        "eight outstanding messages request acknowledgement",
			why:         "reaching the window limit must solicit a cumulative ACK carrying the newest sent sequence",
			ackedSeq:    0,
			wantPing:    true,
			wantPingSeq: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("necessity: %s", tt.why)
			f := newClientFixture(t)
			f.client.sentSeq = 7
			f.client.ackedSeq = tt.ackedSeq

			pingPayload := make(chan []byte, 1)
			f.peer.SetPingHandler(func(payload string) error {
				pingPayload <- []byte(payload)
				return nil
			})
			textSeen := make(chan websocketRead, 1)
			peerReadDone := make(chan error, 1)
			go func() {
				for {
					messageType, payload, err := f.peer.ReadMessage()
					if err != nil {
						peerReadDone <- err
						return
					}
					textSeen <- websocketRead{messageType: messageType, payload: payload}
				}
			}()

			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			wg.Add(1)
			go f.client.writePump(&wg, ctx)
			waitForWritePumpStart()

			publishTestCaption(f.hub.sess, &protocol.SourceHello{
				Type:    protocol.TypeSourceHello,
				Session: "flow-control-boundary",
			})
			select {
			case got := <-textSeen:
				if got.messageType != websocket.TextMessage {
					t.Fatalf("caption frame type = %d, want %d", got.messageType, websocket.TextMessage)
				}
			case <-time.After(clientTestTimeout):
				t.Fatal("peer did not receive caption frame")
			}

			if tt.wantPing {
				select {
				case payload := <-pingPayload:
					gotSequence, err := protocol.DecodeAck(payload)
					if err != nil {
						t.Fatalf("decode flow-control ping: %v", err)
					}
					if gotSequence != tt.wantPingSeq {
						t.Fatalf("flow-control ping sequence = %d, want %d", gotSequence, tt.wantPingSeq)
					}
				case <-time.After(clientTestTimeout):
					t.Fatal("peer did not receive flow-control ping")
				}
			} else {
				select {
				case payload := <-pingPayload:
					t.Fatalf("unexpected flow-control ping payload %x", payload)
				case <-time.After(50 * time.Millisecond):
				}
			}

			cancel()
			waitForWaitGroup(t, &wg)
			select {
			case <-peerReadDone:
			case <-time.After(clientTestTimeout):
				t.Fatal("peer reader did not stop when writePump closed the connection")
			}
		})
	}
}
