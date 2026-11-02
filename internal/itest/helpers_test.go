package itest

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/BarneyLaw/distributary/internal/fanout"
	"github.com/BarneyLaw/distributary/internal/server"
	"github.com/BarneyLaw/distributary/internal/session"
	"github.com/BarneyLaw/distributary/internal/source"
)

// Implemented with Claude
// baseTS is the source_ts of segment 0. Segments are spaced half a second
// apart on the source's clock, so 100 segments fit well inside the 90 s
// retention.
const baseTS = 1760000000.0

// Implemented with Claude
func tsOf(seg int) float64 { return baseTS + float64(seg)*0.5 }

// Implemented with Claude
// stack is one in-process server: the same router, source handler, session
// and fanout hub that main wires together.
type stack struct {
	url string
	src *source.Handler
}

// Implemented with Claude
func newStack(t *testing.T, cfg session.Config) *stack {
	t.Helper()
	sess := session.New(cfg)
	quiet := log.New(io.Discard, "", 0)
	src := source.New(sess, quiet)
	ts := httptest.NewServer(&server.Router{
		Sources:      src,
		Clients:      fanout.NewHub(sess),
		Log:          quiet,
		HelloTimeout: 300 * time.Millisecond,
	})
	t.Cleanup(ts.Close)
	return &stack{url: "ws" + strings.TrimPrefix(ts.URL, "http"), src: src}
}

// Implemented with Claude
func (s *stack) dial() (*websocket.Conn, error) {
	c, _, err := websocket.DefaultDialer.Dial(s.url, nil)
	return c, err
}

// Implemented with Claude
// source connects a caption source and sends SOURCE_HELLO.
func (s *stack) source(t *testing.T) *websocket.Conn {
	t.Helper()
	c, err := s.dial()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	send(t, c, `{"type":"SOURCE_HELLO","session":"L1"}`)
	return c
}

// Implemented with Claude
// waitAccepted blocks until the session has accepted n updates in total, so
// a test knows the source's frames have been applied before it goes on.
func (s *stack) waitAccepted(t *testing.T, n uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for s.src.Stats().Accepted < n {
		if time.Now().After(deadline) {
			t.Fatalf("session accepted %d updates within 2 s, want %d (%s)", s.src.Stats().Accepted, n, s.src.Stats())
		}
		time.Sleep(time.Millisecond)
	}
}

// Implemented with Claude
// join connects a client, sends CLIENT_HELLO and reads the HISTORY reply.
// It returns errors instead of failing, so goroutines can use it.
func (s *stack) join(lastSeg, lastRev int64, historySeconds float64) (*websocket.Conn, []msg, error) {
	c, err := s.dial()
	if err != nil {
		return nil, nil, err
	}
	hello := fmt.Sprintf(`{"type":"CLIENT_HELLO","client_id":1,"session":"L1","last_segment":%d,"last_revision":%d,"history_seconds":%g}`,
		lastSeg, lastRev, historySeconds)
	if err := c.WriteMessage(websocket.TextMessage, []byte(hello)); err != nil {
		c.Close()
		return nil, nil, err
	}
	m, err := read(c, 2*time.Second)
	if err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("reading HISTORY: %w", err)
	}
	if m.Type != "HISTORY" {
		c.Close()
		return nil, nil, fmt.Errorf("first message is %s, want HISTORY", m.Type)
	}
	return c, m.Items, nil
}

// Implemented with Claude
// mustJoin is join for the test goroutine.
func (s *stack) mustJoin(t *testing.T, lastSeg, lastRev int64, historySeconds float64) (*websocket.Conn, []msg) {
	t.Helper()
	c, items, err := s.join(lastSeg, lastRev, historySeconds)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, items
}

// Implemented with Claude
// msg is any frame the server sends to a client.
type msg struct {
	Type     string  `json:"type"`
	Segment  int64   `json:"segment"`
	Revision int64   `json:"revision"`
	SourceTS float64 `json:"source_ts"`
	Final    bool    `json:"final"`
	Text     string  `json:"text"`
	Items    []msg   `json:"items"`

	raw string
}

// Implemented with Claude
func (m msg) String() string {
	if m.Type == "HISTORY" {
		return fmt.Sprintf("HISTORY(%d items)", len(m.Items))
	}
	f := "p"
	if m.Final {
		f = "F"
	}
	return fmt.Sprintf("%d.%d%s", m.Segment, m.Revision, f)
}

// Implemented with Claude
func send(t *testing.T, c *websocket.Conn, frame string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Fatal(err)
	}
}

// Implemented with Claude
func caption(seg, rev int, final bool, text string) string {
	return fmt.Sprintf(`{"type":"CAPTION_UPDATE","session":"L1","segment":%d,"revision":%d,"source_ts":%f,"final":%t,"text":%q}`,
		seg, rev, tsOf(seg), final, text)
}

// Implemented with Claude
// sendSegments sends segments from..to, each as revs-1 provisionals then a
// final at revision revs. It returns the number of updates sent.
func sendSegments(t *testing.T, src *websocket.Conn, from, to, revs int) uint64 {
	t.Helper()
	var n uint64
	for seg := from; seg <= to; seg++ {
		for rev := 1; rev <= revs; rev++ {
			send(t, src, caption(seg, rev, rev == revs, fmt.Sprintf("seg %d rev %d", seg, rev)))
			n++
		}
	}
	return n
}

// Implemented with Claude
// read reads one frame. Reading also lets gorilla answer the server's
// flow-control pings, so a client that stops calling read looks stalled.
func read(c *websocket.Conn, timeout time.Duration) (msg, error) {
	c.SetReadDeadline(time.Now().Add(timeout))
	_, raw, err := c.ReadMessage()
	if err != nil {
		return msg{}, err
	}
	var m msg
	if err := json.Unmarshal(raw, &m); err != nil {
		return msg{}, fmt.Errorf("bad frame %q: %w", raw, err)
	}
	m.raw = string(raw)
	return m, nil
}

// Implemented with Claude
// readUntil reads frames until done returns true for the frames read so far,
// and returns them all.
func readUntil(c *websocket.Conn, timeout time.Duration, done func([]msg) bool) ([]msg, error) {
	deadline := time.Now().Add(timeout)
	var got []msg
	for !done(got) {
		left := time.Until(deadline)
		if left <= 0 {
			return got, fmt.Errorf("timed out after %v, got %v", timeout, got)
		}
		m, err := read(c, left)
		if err != nil {
			return got, fmt.Errorf("%w, got %v", err, got)
		}
		got = append(got, m)
	}
	return got, nil
}

// Implemented with Claude
// untilFinal is a readUntil condition: a final for segment seg has arrived.
func untilFinal(seg int64) func([]msg) bool {
	return func(ms []msg) bool {
		for _, m := range ms {
			if m.Final && m.Segment == seg {
				return true
			}
		}
		return false
	}
}

// Implemented with Claude
// untilRevision is a readUntil condition: segment seg revision rev arrived.
func untilRevision(seg, rev int64) func([]msg) bool {
	return func(ms []msg) bool {
		for _, m := range ms {
			if m.Segment == seg && m.Revision == rev {
				return true
			}
		}
		return false
	}
}

// Implemented with Claude
// checkStream checks the invariants a client must never see broken, over
// everything one client received (HISTORY items and live frames, across
// reconnects if the caller joins them):
//
//   - every frame is a CAPTION_UPDATE
//   - within a segment, revisions strictly increase (no stale, no duplicate)
//   - nothing arrives for a segment after its final
//   - finals arrive in increasing segment order
//
// It returns the segments of the finals, in the order received.
func checkStream(ms []msg) ([]int64, error) {
	type state struct {
		rev   int64
		final bool
	}
	segs := map[int64]*state{}
	var finals []int64
	for i, m := range ms {
		if m.Type != "CAPTION_UPDATE" {
			return nil, fmt.Errorf("frame %d is %s, want CAPTION_UPDATE", i, m.Type)
		}
		st := segs[m.Segment]
		if st == nil {
			st = &state{}
			segs[m.Segment] = st
		}
		switch {
		case st.final:
			return nil, fmt.Errorf("frame %d: %v after segment %d was final (stream %v)", i, m, m.Segment, ms)
		case m.Revision <= st.rev:
			return nil, fmt.Errorf("frame %d: %v after revision %d (stream %v)", i, m, st.rev, ms)
		}
		st.rev, st.final = m.Revision, m.Final
		if m.Final {
			if n := len(finals); n > 0 && m.Segment <= finals[n-1] {
				return nil, fmt.Errorf("frame %d: final %d after final %d (stream %v)", i, m.Segment, finals[n-1], ms)
			}
			finals = append(finals, m.Segment)
		}
	}
	return finals, nil
}

// Implemented with Claude
var errorf = fmt.Errorf

// Implemented with Claude
// seq returns from, from+1, ..., to.
func seq(from, to int64) []int64 {
	var s []int64
	for i := from; i <= to; i++ {
		s = append(s, i)
	}
	return s
}

// Implemented with Claude
func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
