package source

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/BarneyLaw/distributary/internal/server"
	"github.com/BarneyLaw/distributary/internal/session"
)

// noClients refuses every student: these tests only exercise the source.
type noClients struct{}

func (noClients) AddClient(*websocket.Conn, *protocol.ClientHello) error {
	return errors.New("no clients in source tests")
}

// start runs the real router with this package's Handler behind it.
func start(t *testing.T) (*Handler, string) {
	t.Helper()
	h := New(session.New(session.DefaultConfig()), log.New(io.Discard, "", 0))
	ts := httptest.NewServer(&server.Router{Sources: h, Clients: noClients{}, Log: log.New(io.Discard, "", 0)})
	t.Cleanup(ts.Close)
	return h, "ws" + strings.TrimPrefix(ts.URL, "http")
}

// connectSource dials and sends SOURCE_HELLO, like caption_source.py.
func connectSource(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	send(t, c, `{"type":"SOURCE_HELLO","session":"L1"}`)
	return c
}

func send(t *testing.T, c *websocket.Conn, msgs ...string) {
	t.Helper()
	for _, m := range msgs {
		if err := c.WriteMessage(websocket.TextMessage, []byte(m)); err != nil {
			t.Fatalf("write %s: %v", m, err)
		}
	}
}

func caption(seg, rev int, final bool) string {
	return fmt.Sprintf(`{"type":"CAPTION_UPDATE","session":"L1","segment":%d,"revision":%d,"source_ts":1760000000.5,"final":%t,"text":"t"}`,
		seg, rev, final)
}

// waitFor polls Stats until ok returns true. Serve runs on the server's
// goroutine, so counters update shortly after our writes, not immediately.
func waitFor(t *testing.T, h *Handler, what string, ok func(Stats) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok(h.Stats()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: stats are %s", what, h.Stats())
}

func TestCaptionsReachSession(t *testing.T) {
	h, url := start(t)
	c := connectSource(t, url)
	send(t, c, caption(1, 1, false), caption(1, 2, false), caption(1, 3, true))
	waitFor(t, h, "three revisions accepted", func(s Stats) bool { return s.Accepted == 3 })

	// The final is now stored in session: sending it again is rejected.
	send(t, c, caption(1, 3, true))
	waitFor(t, h, "repeated final rejected", func(s Stats) bool { return s.AlreadyFinal == 1 })
}

func TestVerdictsAreCounted(t *testing.T) {
	h, url := start(t)
	c := connectSource(t, url)
	send(t, c, caption(1, 2, false), caption(1, 2, false), caption(1, 1, false))
	waitFor(t, h, "accepted, duplicate, stale", func(s Stats) bool {
		return s.Accepted == 1 && s.Duplicate == 1 && s.Stale == 1
	})
}

func TestBadFramesAreSkippedNotFatal(t *testing.T) {
	h, url := start(t)
	c := connectSource(t, url)
	send(t, c,
		`not json`,
		`{"type":"SOMETHING_ELSE"}`,
		`{"type":"CAPTION_UPDATE","segment":0,"revision":1,"source_ts":1,"final":true,"text":"bad segment"}`,
		caption(1, 1, true),
	)
	waitFor(t, h, "3 malformed, then 1 accepted", func(s Stats) bool { return s.Malformed == 3 && s.Accepted == 1 })

	send(t, c, caption(2, 1, true))
	waitFor(t, h, "connection still open after bad frames", func(s Stats) bool { return s.Accepted == 2 })
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	h, url := start(t)
	c := connectSource(t, url)
	send(t, c, `{"type":"CAPTION_UPDATE","session":"L1","segment":1,"revision":1,"source_ts":1,"final":true,"text":"t","phase":"warmup"}`)
	waitFor(t, h, "caption with an extra field accepted", func(s Stats) bool { return s.Accepted == 1 })
}

func TestSourceDoneKeepsStreamOpen(t *testing.T) {
	h, url := start(t)
	c := connectSource(t, url)
	send(t, c, `{"type":"SOURCE_DONE","session":"L1","segments":0}`, caption(1, 1, true))
	waitFor(t, h, "SOURCE_DONE then a caption", func(s Stats) bool { return s.Done == 1 && s.Accepted == 1 })
}

func TestNewSourceReplacesOld(t *testing.T) {
	h, url := start(t)
	a := connectSource(t, url)
	send(t, a, caption(1, 1, true))
	waitFor(t, h, "source A accepted", func(s Stats) bool { return s.Accepted == 1 })

	b := connectSource(t, url)

	// A is closed by the server. Our client only notices when it reads.
	a.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := a.ReadMessage(); err == nil {
		t.Fatal("source A should have been disconnected when B connected")
	} else if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
		t.Fatal("source A is still connected after B took over")
	}

	send(t, b, caption(2, 1, true))
	waitFor(t, h, "source B accepted", func(s Stats) bool { return s.Accepted == 2 })
}

func TestNothingIsSentToTheSource(t *testing.T) {
	h, url := start(t)
	c := connectSource(t, url)
	send(t, c, caption(1, 1, false), caption(1, 2, true), `{"type":"SOURCE_DONE"}`)
	waitFor(t, h, "captions processed", func(s Stats) bool { return s.Accepted == 2 && s.Done == 1 })

	c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, raw, err := c.ReadMessage()
	if err == nil {
		t.Fatalf("the server sent the source a message: %s", raw)
	}
	if ne, ok := err.(interface{ Timeout() bool }); !ok || !ne.Timeout() {
		t.Fatalf("expected silence (read timeout), got %v", err)
	}
}
