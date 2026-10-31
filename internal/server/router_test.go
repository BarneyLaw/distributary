package server

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/BarneyLaw/distributary/internal/protocol"
)

// fakeSource records the hello and, if readExtra is set, reads one more
// message after waiting, to show it owns the connection.
type fakeSource struct {
	hellos    chan *protocol.SourceHello
	extra     chan string
	readExtra time.Duration
}

func (f *fakeSource) Handle(ws *websocket.Conn, hello *protocol.SourceHello) {
	f.hellos <- hello
	if f.readExtra > 0 {
		time.Sleep(f.readExtra)
		if _, raw, err := ws.ReadMessage(); err == nil {
			f.extra <- string(raw)
		}
	}
}

// fakeClients records the hello and writes one frame, or refuses with err.
type fakeClients struct {
	hellos chan *protocol.ClientHello
	err    error
}

func (f *fakeClients) AddClient(ws *websocket.Conn, hello *protocol.ClientHello) error {
	if f.err != nil {
		return f.err
	}
	f.hellos <- hello
	return ws.WriteMessage(websocket.TextMessage, []byte("owned by client handler"))
}

func newRouter() (*Router, *fakeSource, *fakeClients) {
	src := &fakeSource{hellos: make(chan *protocol.SourceHello, 1), extra: make(chan string, 1)}
	cl := &fakeClients{hellos: make(chan *protocol.ClientHello, 1)}
	return &Router{Sources: src, Clients: cl, Log: log.New(io.Discard, "", 0)}, src, cl
}

func start(t *testing.T, h http.Handler) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

func dial(t *testing.T, httpURL string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpURL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	return c
}

func send(t *testing.T, c *websocket.Conn, msg string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func wantClose(t *testing.T, c *websocket.Conn, code int) {
	t.Helper()
	_, _, err := c.ReadMessage()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != code {
		t.Fatalf("want close code %d, got %v", code, err)
	}
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		var zero T
		t.Fatalf("%s: handler was never called", what)
		return zero
	}
}

func TestSourceHelloGoesToSourceHandler(t *testing.T) {
	rt, src, cl := newRouter()
	c := dial(t, start(t, rt))
	send(t, c, `{"type":"SOURCE_HELLO","session":"L1"}`)

	if h := receive(t, src.hellos, "source"); h.Session != "L1" {
		t.Fatalf("source hello session = %q, want L1", h.Session)
	}
	if len(cl.hellos) != 0 {
		t.Fatal("client handler must not be called for a source")
	}
}

func TestClientHelloGoesToClientHandler(t *testing.T) {
	rt, src, cl := newRouter()
	c := dial(t, start(t, rt))
	send(t, c, `{"type":"CLIENT_HELLO","client_id":17,"session":"L1","last_segment":38,"last_revision":3,"history_seconds":90}`)

	h := receive(t, cl.hellos, "client")
	if h.ClientID != 17 || h.LastSegment != 38 || h.HistorySeconds != 60 {
		t.Fatalf("got %+v; want client 17, last_segment 38, history_seconds clamped to 60", h)
	}
	if _, raw, err := c.ReadMessage(); err != nil || string(raw) != "owned by client handler" {
		t.Fatalf("the client handler must own the connection after dispatch: %q, %v", raw, err)
	}
	if len(src.hellos) != 0 {
		t.Fatal("source handler must not be called for a client")
	}
}

func TestBadHelloIsClosedWithPolicyViolation(t *testing.T) {
	for name, msg := range map[string]string{
		"not JSON":           `hello`,
		"unknown type":       `{"type":"HELLO"}`,
		"caption as hello":   `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true,"text":"x"}`,
		"invalid resume":     `{"type":"CLIENT_HELLO","last_segment":-1}`,
		"history, not hello": `{"type":"HISTORY","items":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			rt, src, cl := newRouter()
			c := dial(t, start(t, rt))
			send(t, c, msg)
			wantClose(t, c, closePolicyViolation)
			if len(src.hellos)+len(cl.hellos) != 0 {
				t.Fatal("no handler may be called for a bad hello")
			}
		})
	}
}

func TestMissingHelloTimesOut(t *testing.T) {
	rt, _, _ := newRouter()
	rt.HelloTimeout = 100 * time.Millisecond
	c := dial(t, start(t, rt))
	wantClose(t, c, closePolicyViolation)
}

func TestHelloDeadlineIsClearedForTheHandler(t *testing.T) {
	// The handler reads its next message well after HelloTimeout. That must
	// still work: the hello deadline must not carry over into the handler.
	rt, src, _ := newRouter()
	rt.HelloTimeout = 100 * time.Millisecond
	src.readExtra = 300 * time.Millisecond
	c := dial(t, start(t, rt))
	send(t, c, `{"type":"SOURCE_HELLO","session":"L1"}`)
	receive(t, src.hellos, "source")

	time.Sleep(150 * time.Millisecond)
	send(t, c, `{"type":"SOURCE_DONE"}`)
	if got := receive(t, src.extra, "source second message"); got != `{"type":"SOURCE_DONE"}` {
		t.Fatalf("handler read %q", got)
	}
}

func TestRefusedClientIsClosedGoingAway(t *testing.T) {
	rt, _, cl := newRouter()
	cl.err = errors.New("hub shut down")
	c := dial(t, start(t, rt))
	send(t, c, `{"type":"CLIENT_HELLO","client_id":1}`)
	wantClose(t, c, closeGoingAway)
}

func TestPlainHTTPRequests(t *testing.T) {
	rt, _, _ := newRouter()
	url := start(t, rt)

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("without Static: status %d, want 426", resp.StatusCode)
	}

	rt.Static = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "client page")
	})
	resp, err = http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "client page" {
		t.Fatalf("with Static: got %q", body)
	}
}
