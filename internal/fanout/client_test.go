package fanout

import (
	"context"
	"errors"
	"net"
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
	h := NewHub(session.New(session.Config{}))
	h.pingInterval = time.Hour
	h.pongTimeout = time.Second
	h.controlTimeout = time.Second

	c := &client{
		hub:  h,
		conn: serverConn,
		hello: &protocol.ClientHello{
			Type:           protocol.TypeClientHello,
			ClientID:       1,
			Session:        "client-test-session",
			HistorySeconds: 30,
		},
		timeSent:       time.Time{},
		timeAcked:      time.Time{},
		sampleRTT:      0 * time.Millisecond,
		sRTT:           0 * time.Millisecond,
		minRTT:         time.Hour,
		queueDelay:     0 * time.Millisecond,
		thresholdCount: 0,
		isDegraded:     false,
		acked:          make(chan struct{}),
	}
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
func clientTestCaption(segment, revision int64, final bool) *protocol.CaptionUpdate {
	return &protocol.CaptionUpdate{
		Type:     protocol.TypeCaptionUpdate,
		Session:  "client-test-session",
		Segment:  segment,
		Revision: revision,
		SourceTS: 100 + float64(segment),
		Final:    final,
		Text:     "caption",
	}
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

func TestClientRTTEstimatorTracksMinimumAndSmoothedRTT(t *testing.T) {
	// Implemented with Codex
	// The minimum must remain the best connection-lifetime sample, while SRTT
	// follows the RFC-style 1/8 EWMA used by the queue-delay detector.
	c := &client{minRTT: time.Hour}
	tests := []struct {
		name     string
		sample   time.Duration
		wantMin  time.Duration
		wantSRTT time.Duration
	}{
		{
			name:     "first sample initializes both estimators",
			sample:   40 * time.Millisecond,
			wantMin:  40 * time.Millisecond,
			wantSRTT: 40 * time.Millisecond,
		},
		{
			name:     "higher sample only raises the smoothed estimate",
			sample:   120 * time.Millisecond,
			wantMin:  40 * time.Millisecond,
			wantSRTT: 50 * time.Millisecond,
		},
		{
			name:     "new minimum lowers the baseline immediately",
			sample:   20 * time.Millisecond,
			wantMin:  20 * time.Millisecond,
			wantSRTT: 46*time.Millisecond + 250*time.Microsecond,
		},
	}

	for _, tt := range tests {
		c.sampleRTT = tt.sample
		c.computeMinRTT()
		c.computeSRTT(0.125)

		if c.minRTT != tt.wantMin {
			t.Fatalf("%s: minRTT = %v, want %v", tt.name, c.minRTT, tt.wantMin)
		}
		if c.sRTT != tt.wantSRTT {
			t.Fatalf("%s: sRTT = %v, want %v", tt.name, c.sRTT, tt.wantSRTT)
		}
	}
}

func TestClientQueueDelayEntersDegradedAndRecoversFromRawSample(t *testing.T) {
	// Implemented with Codex
	// Entry deliberately uses consecutive smoothed queue-delay observations,
	// while recovery uses the latest raw sample so it does not wait for the
	// EWMA to decay after the client's queue drains.
	c := &client{
		hub: &hub{
			queueDelayTreshold: 30 * time.Millisecond,
			modeThreshold:      2,
		},
		minRTT: time.Hour,
	}

	observe := func(sample time.Duration) {
		t.Helper()
		c.sampleRTT = sample
		c.computeMinRTT()
		c.computeSRTT(0.125)
		c.computeQueueDelay()
		c.determineStatus()
	}

	observe(20 * time.Millisecond)
	observe(20 * time.Millisecond)
	if c.isDegraded {
		t.Fatal("client became degraded after baseline RTT observations")
	}

	observe(500 * time.Millisecond)
	if c.queueDelay != 60*time.Millisecond {
		t.Fatalf("first high queueDelay = %v, want %v", c.queueDelay, 60*time.Millisecond)
	}
	if c.thresholdCount != 1 || c.isDegraded {
		t.Fatalf("after first high observation: count=%d degraded=%t, want count=1 degraded=false", c.thresholdCount, c.isDegraded)
	}

	observe(500 * time.Millisecond)
	if c.queueDelay != 112*time.Millisecond+500*time.Microsecond {
		t.Fatalf("second high queueDelay = %v, want %v", c.queueDelay, 112*time.Millisecond+500*time.Microsecond)
	}
	if c.thresholdCount != 2 || !c.isDegraded {
		t.Fatalf("after second high observation: count=%d degraded=%t, want count=2 degraded=true", c.thresholdCount, c.isDegraded)
	}

	observe(500 * time.Millisecond)
	if !c.isDegraded {
		t.Fatal("client recovered while its raw queue delay was still high")
	}

	observe(25 * time.Millisecond)
	sampleQueueDelay := c.sampleRTT - c.minRTT
	if sampleQueueDelay != 5*time.Millisecond {
		t.Fatalf("recovery sample queue delay = %v, want %v", sampleQueueDelay, 5*time.Millisecond)
	}
	if c.queueDelay <= c.hub.queueDelayTreshold {
		t.Fatalf("smoothed queue delay = %v, want it still above %v to prove raw-sample recovery", c.queueDelay, c.hub.queueDelayTreshold)
	}
	if c.isDegraded {
		t.Fatal("client did not recover on a low raw queue-delay sample")
	}
}

func TestClientAdvancingPongRecordsRTTSample(t *testing.T) {
	// Implemented with Codex
	// This integration check guards the time.Duration unit conversion at the
	// pong boundary and proves the first sample initializes all RTT baselines.
	f := newClientFixture(t)
	f.client.sentSeq = 1
	f.client.timeSent = time.Now().Add(-50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.readPump(&wg, cancel)

	if err := f.peer.WriteControl(websocket.PongMessage, protocol.EncodeAck(1), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("send acknowledgement pong: %v", err)
	}
	if err := f.peer.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("send close frame: %v", err)
	}
	waitForWaitGroup(t, &wg)

	f.client.mu.Lock()
	sampleRTT := f.client.sampleRTT
	minRTT := f.client.minRTT
	sRTT := f.client.sRTT
	queueDelay := f.client.queueDelay
	f.client.mu.Unlock()

	if sampleRTT < 50*time.Millisecond {
		t.Fatalf("sampleRTT = %v, want at least %v", sampleRTT, 50*time.Millisecond)
	}
	if sampleRTT >= clientTestTimeout {
		t.Fatalf("sampleRTT = %v, want less than test timeout %v", sampleRTT, clientTestTimeout)
	}
	if minRTT != sampleRTT || sRTT != sampleRTT {
		t.Fatalf("first RTT estimates = min %v, smoothed %v; want both %v", minRTT, sRTT, sampleRTT)
	}
	if queueDelay != 0 {
		t.Fatalf("first queueDelay = %v, want 0", queueDelay)
	}
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("readPump context error = %v, want %v", err, context.Canceled)
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

func TestClientWritePumpSendsKeepalivePingWhenIdle(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because an idle client has no caption traffic to
	// prove liveness; writePump must emit a keepalive control ping while it waits
	// for new session state.
	f := newClientFixture(t)
	f.hub.pingInterval = 5 * time.Millisecond

	pingPayload := make(chan string, 1)
	f.peer.SetPingHandler(func(payload string) error {
		pingPayload <- payload
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.writePump(&wg, ctx)

	history := requireWebsocketRead(t, readWebsocketAsync(f.peer))
	if history.err != nil {
		t.Fatalf("read initial HISTORY: %v", history.err)
	}
	if string(history.payload) != string(protocol.EncodeHistory(nil)) {
		t.Fatalf("initial payload = %s, want empty HISTORY", history.payload)
	}

	// Control frames are processed while a data-frame read is in progress.
	peerRead := readWebsocketAsync(f.peer)

	select {
	case payload := <-pingPayload:
		if payload != "" {
			t.Fatalf("keepalive payload = %x, want empty payload", payload)
		}
	case <-time.After(clientTestTimeout):
		t.Fatal("idle writePump did not send a keepalive ping")
	}

	cancel()
	waitForWaitGroup(t, &wg)
	_ = requireWebsocketRead(t, peerRead)
}

func TestClientWritePumpStopsWhenKeepalivePingFails(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because writePump now owns keepalive writes and must
	// terminate when an idle connection can no longer accept a control frame.
	f := newClientFixture(t)
	f.hub.pingInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.writePump(&wg, ctx)

	history := requireWebsocketRead(t, readWebsocketAsync(f.peer))
	if history.err != nil {
		t.Fatalf("read initial HISTORY: %v", history.err)
	}

	// Closing the server side makes the next keepalive WriteControl fail. The
	// writer has no other pending work that could make it exit first.
	f.client.close()
	waitForWaitGroup(t, &wg)
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

func TestClientWritePumpSendsHistoryThenLiveCaption(t *testing.T) {
	// Implemented with Codex
	// This test is necessary because the writer must pass its ClientHello to
	// Session.Join, send the resulting HISTORY first, then use the returned
	// cursor to send later Session.Pick frames without a gap or duplicate.
	f := newClientFixture(t)
	f.client.hello.LastSegment = 1
	f.client.hello.HistorySeconds = 60
	alreadyHeld := clientTestCaption(1, 1, true)
	historical := clientTestCaption(2, 1, true)
	for _, caption := range []*protocol.CaptionUpdate{alreadyHeld, historical} {
		if verdict := f.hub.sess.Apply(caption); verdict != session.Accepted {
			t.Fatalf("apply historical caption: got %v, want %v", verdict, session.Accepted)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.writePump(&wg, ctx)

	historyFrame := requireWebsocketRead(t, readWebsocketAsync(f.peer))
	if historyFrame.err != nil {
		t.Fatalf("read HISTORY frame: %v", historyFrame.err)
	}
	if historyFrame.messageType != websocket.TextMessage {
		t.Fatalf("HISTORY frame type = %d, want %d", historyFrame.messageType, websocket.TextMessage)
	}
	wantHistory := protocol.EncodeHistory([][]byte{historical.Encode()})
	if string(historyFrame.payload) != string(wantHistory) {
		t.Fatalf("HISTORY payload = %s, want %s", historyFrame.payload, wantHistory)
	}

	live := clientTestCaption(3, 1, false)
	liveRead := readWebsocketAsync(f.peer)
	if verdict := f.hub.sess.Apply(live); verdict != session.Accepted {
		t.Fatalf("apply live caption: got %v, want %v", verdict, session.Accepted)
	}
	liveFrame := requireWebsocketRead(t, liveRead)
	if liveFrame.err != nil {
		t.Fatalf("read live caption: %v", liveFrame.err)
	}
	if liveFrame.messageType != websocket.TextMessage {
		t.Fatalf("live frame type = %d, want %d", liveFrame.messageType, websocket.TextMessage)
	}
	if string(liveFrame.payload) != string(live.Encode()) {
		t.Fatalf("live payload = %s, want %s", liveFrame.payload, live.Encode())
	}

	// The peer can receive bytes just before WriteMessage returns to the
	// sender, so wait for the immediately-following accounting update.
	requireSentSequence(t, f.client, 2)

	cancel()
	waitForWaitGroup(t, &wg)
}

func TestClientWritePumpClosesTryAgainLaterWhenCursorFallsBehind(t *testing.T) {
	// Implemented with Codex
	// A client whose next required final has left the bounded session ring
	// cannot continue its current cursor safely. The writer must identify this
	// as a recoverable overload condition so the peer can reconnect with its
	// finalized resume position and obtain HISTORY.
	f := newClientFixture(t)
	f.hub.sess = session.New(session.Config{
		Retention: time.Hour,
		MaxFinals: 2,
		MaxActive: 16,
	})
	f.hub.maxInFlight = 1
	f.hub.pongTimeout = time.Second

	// Hold the writer at the flow-control boundary after HISTORY. This makes
	// the subsequent ring eviction deterministic instead of racing writePump.
	flowPing := make(chan string, 1)
	f.peer.SetPingHandler(func(payload string) error {
		flowPing <- payload
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var pumps sync.WaitGroup
	pumps.Add(2)
	go f.client.readPump(&pumps, cancel)
	go f.client.writePump(&pumps, ctx)

	history := requireWebsocketRead(t, readWebsocketAsync(f.peer))
	if history.err != nil {
		t.Fatalf("read initial HISTORY: %v", history.err)
	}
	if history.messageType != websocket.TextMessage || string(history.payload) != string(protocol.EncodeHistory(nil)) {
		t.Fatalf("initial frame = type %d payload %s, want empty HISTORY", history.messageType, history.payload)
	}

	closed := readWebsocketAsync(f.peer)
	var pingPayload string
	select {
	case pingPayload = <-flowPing:
	case <-time.After(clientTestTimeout):
		t.Fatal("writer did not reach the flow-control boundary")
	}

	for segment := int64(1); segment <= 3; segment++ {
		caption := clientTestCaption(segment, 1, true)
		if verdict := f.hub.sess.Apply(caption); verdict != session.Accepted {
			t.Fatalf("apply final segment %d: got %v, want %v", segment, verdict, session.Accepted)
		}
	}

	if err := f.peer.WriteControl(
		websocket.PongMessage,
		[]byte(pingPayload),
		time.Now().Add(time.Second),
	); err != nil {
		t.Fatalf("release flow-controlled writer: %v", err)
	}

	result := requireWebsocketRead(t, closed)
	if result.err == nil {
		t.Fatalf("peer read unexpectedly succeeded: type=%d payload=%q", result.messageType, result.payload)
	}
	var closeErr *websocket.CloseError
	if !errors.As(result.err, &closeErr) {
		t.Fatalf("peer error = %T %v, want *websocket.CloseError", result.err, result.err)
	}
	if closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("close code = %d, want %d", closeErr.Code, websocket.CloseTryAgainLater)
	}
	const wantReason = "Client fell behind retained history"
	if closeErr.Text != wantReason {
		t.Fatalf("close reason = %q, want %q", closeErr.Text, wantReason)
	}

	waitForWaitGroup(t, &pumps)
}

func TestClientWritePumpDegradedModeSuppressesProvisionalButSendsFinal(t *testing.T) {
	// Implemented with Codex
	// Degraded mode is useful only if the status selected by the RTT detector
	// reaches Session.Pick: obsolete provisional work must stop, while durable
	// finals must continue to make progress.
	f := newClientFixture(t)
	f.client.isDegraded = true

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.writePump(&wg, ctx)

	history := requireWebsocketRead(t, readWebsocketAsync(f.peer))
	if history.err != nil {
		t.Fatalf("read initial HISTORY: %v", history.err)
	}
	if string(history.payload) != string(protocol.EncodeHistory(nil)) {
		t.Fatalf("initial payload = %s, want empty HISTORY", history.payload)
	}

	nextFrame := readWebsocketAsync(f.peer)
	provisional := clientTestCaption(1, 1, false)
	if verdict := f.hub.sess.Apply(provisional); verdict != session.Accepted {
		t.Fatalf("apply provisional caption: got %v, want %v", verdict, session.Accepted)
	}
	select {
	case got := <-nextFrame:
		t.Fatalf("degraded client received provisional frame: type=%d payload=%s err=%v", got.messageType, got.payload, got.err)
	case <-time.After(50 * time.Millisecond):
	}

	final := clientTestCaption(1, 2, true)
	if verdict := f.hub.sess.Apply(final); verdict != session.Accepted {
		t.Fatalf("apply final caption: got %v, want %v", verdict, session.Accepted)
	}
	got := requireWebsocketRead(t, nextFrame)
	if got.err != nil {
		t.Fatalf("read final caption: %v", got.err)
	}
	if got.messageType != websocket.TextMessage || string(got.payload) != string(final.Encode()) {
		t.Fatalf("degraded frame = type %d payload %s, want final %s", got.messageType, got.payload, final.Encode())
	}

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
	defer cancelWriter()

	wantPayload := []byte(`{"caption":"below-window"}`)
	if err := f.client.write(wantPayload, writerCtx); err != nil {
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

	err := f.client.write([]byte("unwritten"), context.Background())
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
	// acked channel: the fourth outstanding frame must ping, wait, then resume
	// only after a valid cumulative pong advances ackedSeq.
	f := newClientFixture(t)
	f.client.sentSeq = 3
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
	defer cancelWriter()
	writeResult := make(chan error, 1)
	wantPayload := []byte(`{"caption":"window-limit"}`)
	go func() {
		writeResult <- f.client.write(wantPayload, writerCtx)
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
		if sequence != 4 {
			t.Fatalf("flow-control ping sequence = %d, want 4", sequence)
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
	if gotSentSeq != 4 || gotAckedSeq != 4 {
		t.Fatalf("sequence state = sent %d, acked %d; want sent 4, acked 4", gotSentSeq, gotAckedSeq)
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

func TestClientFlowControlDistinguishesProgressingSlowFromStalled(t *testing.T) {
	// Implemented with Codex
	// A slow client may take a substantial fraction of pongTimeout to advance
	// each ACK. Its connection must survive for longer than one pongTimeout as
	// long as every flow-control pause eventually observes forward progress. A
	// client whose ACK never advances must cause writePump to close the socket.
	t.Run("continually progressing slow client remains connected", func(t *testing.T) {
		f := newClientFixture(t)
		f.hub.maxInFlight = 1
		f.hub.pongTimeout = 200 * time.Millisecond

		const rounds = 4
		const pongDelay = 60 * time.Millisecond
		f.peer.SetPingHandler(func(payload string) error {
			time.Sleep(pongDelay)
			return f.peer.WriteControl(
				websocket.PongMessage,
				[]byte(payload),
				time.Now().Add(time.Second),
			)
		})

		peerReadDone := make(chan error, 1)
		go func() {
			for {
				if _, _, err := f.peer.ReadMessage(); err != nil {
					peerReadDone <- err
					return
				}
			}
		}()

		readerCtx, cancelReader := context.WithCancel(context.Background())
		defer cancelReader()
		var readerWG sync.WaitGroup
		readerWG.Add(1)
		go f.client.readPump(&readerWG, cancelReader)

		started := time.Now()
		for i := 0; i < rounds; i++ {
			if err := f.client.write([]byte(`{"caption":"slow-progress"}`), readerCtx); err != nil {
				t.Fatalf("write %d rejected progressing slow client: %v", i+1, err)
			}
		}
		elapsed := time.Since(started)
		if elapsed < f.hub.pongTimeout {
			t.Fatalf("writes completed in %v, want at least one dead-client timeout %v", elapsed, f.hub.pongTimeout)
		}

		f.client.mu.Lock()
		gotSentSeq := f.client.sentSeq
		gotAckedSeq := f.client.ackedSeq
		f.client.mu.Unlock()
		if gotSentSeq != rounds || gotAckedSeq != rounds {
			t.Fatalf("sequence state = sent %d, acked %d; want sent %d, acked %d", gotSentSeq, gotAckedSeq, rounds, rounds)
		}
		if got := f.transportCloseCalls.Load(); got != 0 {
			t.Fatalf("progressing slow client was disconnected after %d transport closes", got)
		}

		if err := f.peer.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second)); err != nil {
			t.Fatalf("close progressing peer: %v", err)
		}
		waitForWaitGroup(t, &readerWG)
		select {
		case <-peerReadDone:
		case <-time.After(clientTestTimeout):
			t.Fatal("progressing peer reader did not stop during cleanup")
		}
	})

	t.Run("stalled client is disconnected", func(t *testing.T) {
		f := newClientFixture(t)
		f.hub.maxInFlight = 1
		f.hub.pongTimeout = 150 * time.Millisecond

		pingSeen := make(chan struct{}, 1)
		f.peer.SetPingHandler(func(string) error {
			pingSeen <- struct{}{}
			return nil // Deliberately do not advance the ACK.
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var writerWG sync.WaitGroup
		writerWG.Add(1)
		go f.client.writePump(&writerWG, ctx)

		history := requireWebsocketRead(t, readWebsocketAsync(f.peer))
		if history.err != nil {
			t.Fatalf("read initial HISTORY: %v", history.err)
		}
		if string(history.payload) != string(protocol.EncodeHistory(nil)) {
			t.Fatalf("initial payload = %s, want empty HISTORY", history.payload)
		}

		closed := readWebsocketAsync(f.peer)
		select {
		case <-pingSeen:
		case <-time.After(clientTestTimeout):
			t.Fatal("stalled peer did not receive a flow-control ping")
		}

		waitForWaitGroup(t, &writerWG)
		result := requireWebsocketRead(t, closed)
		if result.err == nil {
			t.Fatal("stalled peer remained connected after its ACK stopped advancing")
		}
		if got := f.transportCloseCalls.Load(); got != 1 {
			t.Fatalf("transport close calls = %d, want 1 after stalled-client timeout", got)
		}
	})
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
			defer cancelWriter()

			pingPayload := make(chan []byte, 1)
			f.peer.SetPingHandler(func(payload string) error {
				pingPayload <- []byte(payload)
				return nil
			})
			peerRead := readWebsocketAsync(f.peer)
			result := make(chan error, 1)
			go func() {
				result <- f.client.startPingingMode(17, writerCtx, acked)
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
	f.hub.pongTimeout = 20 * time.Millisecond
	peerRead := readWebsocketAsync(f.peer)

	err := f.client.startPingingMode(
		8,
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

			// writePump always sends HISTORY before it waits on lifecycle
			// signals. Reading it is also a deterministic readiness barrier.
			history := requireWebsocketRead(t, readWebsocketAsync(f.peer))
			if history.err != nil {
				t.Fatalf("read initial HISTORY: %v", history.err)
			}
			if string(history.payload) != string(protocol.EncodeHistory(nil)) {
				t.Fatalf("initial payload = %s, want empty HISTORY", history.payload)
			}

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
	// This test is necessary because failure to write the mandatory initial
	// HISTORY must terminate the pump before it enters its keepalive wait loop.
	f := newClientFixture(t)
	f.client.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go f.client.writePump(&wg, ctx)
	waitForWaitGroup(t, &wg)
	if got := f.client.sentSeq; got != 0 {
		t.Fatalf("sentSeq = %d after failed HISTORY write, want 0", got)
	}
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
			name:        "three outstanding messages do not ping",
			why:         "the client should use all three slots below the configured threshold without extra control traffic",
			ackedSeq:    1,
			wantPing:    false,
			wantPingSeq: 0,
		},
		{
			name:        "four outstanding messages request acknowledgement",
			why:         "reaching the window limit must solicit a cumulative ACK carrying the newest sent sequence",
			ackedSeq:    0,
			wantPing:    true,
			wantPingSeq: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("necessity: %s", tt.why)
			f := newClientFixture(t)
			f.client.sentSeq = 3
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

			// The mandatory HISTORY is the fourth sent frame in this setup,
			// so it exercises the boundary without requiring session updates.
			select {
			case got := <-textSeen:
				if got.messageType != websocket.TextMessage {
					t.Fatalf("HISTORY frame type = %d, want %d", got.messageType, websocket.TextMessage)
				}
				if string(got.payload) != string(protocol.EncodeHistory(nil)) {
					t.Fatalf("initial payload = %s, want empty HISTORY", got.payload)
				}
			case <-time.After(clientTestTimeout):
				t.Fatal("peer did not receive initial HISTORY")
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
