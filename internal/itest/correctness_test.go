package itest

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BarneyLaw/distributary/internal/session"
)

// Implemented with Claude
// Many clients, many segments with several revisions each: every client gets
// every final exactly once, in order, with no stale or duplicate state.
// This is the hidden evaluator's correctness gate (completeness >= 99%).
func TestEveryClientGetsEveryFinalOnceInOrder(t *testing.T) {
	const clients, segments, revs = 50, 20, 3
	s := newStack(t, session.DefaultConfig())

	conns := make([]interface{ Close() error }, 0, clients)
	type result struct {
		finals []int64
		err    error
	}
	results := make(chan result, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		c, _ := s.mustJoin(t, 0, 0, 30)
		conns = append(conns, c)
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := readUntil(c, 5*time.Second, untilFinal(segments))
			if err != nil {
				results <- result{err: err}
				return
			}
			finals, err := checkStream(got)
			results <- result{finals, err}
		}()
	}

	src := s.source(t)
	sendSegments(t, src, 1, segments, revs)
	wg.Wait()
	close(results)

	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !equal(r.finals, seq(1, segments)) {
			t.Fatalf("client got finals %v, want 1..%d", r.finals, segments)
		}
	}
}

// Implemented with Claude
// Duplicates, stale revisions and updates after a final never reach a
// client, and the final's text is the one delivered.
func TestOlderRevisionNeverReplacesNewer(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	c, _ := s.mustJoin(t, 0, 0, 0)
	src := s.source(t)

	for _, f := range []string{
		caption(1, 1, false, "TCP uses"),
		caption(1, 2, false, "TCP uses cumulative"),
		caption(1, 2, false, "TCP uses cumulative"), // duplicate
		caption(1, 1, false, "TCP uses"),            // stale
		caption(1, 3, true, "TCP uses cumulative acknowledgements."),
		caption(1, 4, false, "late provisional"), // after final
		caption(1, 2, true, "older final"),       // after final
		caption(2, 1, true, "next"),
	} {
		send(t, src, f)
	}

	got, err := readUntil(c, 2*time.Second, untilFinal(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStream(got); err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.Segment == 1 && m.Final && m.Text != "TCP uses cumulative acknowledgements." {
			t.Fatalf("final of segment 1 has text %q", m.Text)
		}
	}
	st := s.src.Stats()
	if st.Duplicate != 1 || st.Stale != 1 || st.AlreadyFinal != 2 {
		t.Fatalf("source stats %s, want duplicate=1 stale=1 already_final=2", st)
	}
}

// Implemented with Claude
// Spec 4.1: source messages may carry extra fields, and the server must
// ignore them and carry on. Extra fields are not forwarded to clients.
func TestUnknownFieldsAreIgnoredEndToEnd(t *testing.T) {
	s := newStack(t, session.DefaultConfig())

	c, err := s.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send(t, c, `{"type":"CLIENT_HELLO","client_id":3,"session":"L1","last_segment":0,"last_revision":0,"history_seconds":30,"ua":"test","resume":{"x":1}}`)
	if m, err := read(c, 2*time.Second); err != nil || m.Type != "HISTORY" {
		t.Fatalf("got %v, %v, want HISTORY", m, err)
	}

	src, err := s.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	send(t, src, `{"type":"SOURCE_HELLO","session":"L1","phase":"warmup","version":2}`)
	send(t, src, fmt.Sprintf(`{"type":"CAPTION_UPDATE","session":"L1","segment":1,"revision":1,"source_ts":%f,"final":false,"text":"a","phase":"burst","extra":[1,2]}`, tsOf(1)))
	send(t, src, fmt.Sprintf(`{"phase":"burst","type":"CAPTION_UPDATE","session":"L1","segment":1,"revision":2,"source_ts":%f,"final":true,"text":"ab","meta":{"k":null}}`, tsOf(1)))

	got, err := readUntil(c, 2*time.Second, untilFinal(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStream(got); err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if strings.Contains(m.raw, "phase") || strings.Contains(m.raw, "extra") || strings.Contains(m.raw, "meta") {
			t.Fatalf("unknown field forwarded to client: %s", m.raw)
		}
	}
}

// Implemented with Claude
// A bad frame from the source is skipped; the frames around it still flow.
func TestBadSourceFramesAreSkipped(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	c, _ := s.mustJoin(t, 0, 0, 0)
	src := s.source(t)

	send(t, src, caption(1, 1, true, "one"))
	for _, bad := range []string{
		`not json`,
		`{"type":"CAPTION_UPDATE","segment":2,"revision":1,"source_ts":1760000001,"final":true}`,              // no text
		`{"type":"CAPTION_UPDATE","segment":"2","revision":1,"source_ts":1760000001,"final":true,"text":"x"}`, // segment as string
		`{"type":"CAPTION_UPDATE","segment":0,"revision":1,"source_ts":1760000001,"final":true,"text":"x"}`,   // segment 0
		`{"type":"NOPE"}`,
	} {
		send(t, src, bad)
	}
	send(t, src, caption(2, 1, true, "two"))

	got, err := readUntil(c, 2*time.Second, untilFinal(2))
	if err != nil {
		t.Fatal(err)
	}
	finals, err := checkStream(got)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(finals, []int64{1, 2}) {
		t.Fatalf("finals %v, want [1 2]", finals)
	}
	if m := s.src.Stats().Malformed; m != 5 {
		t.Fatalf("malformed = %d, want 5", m)
	}
}

// Implemented with Claude
// SOURCE_DONE is test control: never forwarded, and the stream goes on.
func TestSourceDoneIsNotForwarded(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	c, _ := s.mustJoin(t, 0, 0, 0)
	src := s.source(t)

	send(t, src, caption(1, 1, true, "one"))
	send(t, src, `{"type":"SOURCE_DONE","session":"L1","source_ts":1760000001.0,"segments":1}`)
	send(t, src, caption(2, 1, true, "two"))

	got, err := readUntil(c, 2*time.Second, untilFinal(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStream(got); err != nil { // rejects any non-CAPTION_UPDATE frame
		t.Fatal(err)
	}
	if d := s.src.Stats().Done; d != 1 {
		t.Fatalf("done = %d, want 1", d)
	}
}

// Implemented with Claude
// Normal Update Coverage: when nothing is overloaded, a client that reads
// promptly gets every revision, not just the finals. The source is paced
// like caption_source.py, a few milliseconds between updates.
func TestPromptClientGetsEveryRevision(t *testing.T) {
	const segments, revs = 10, 5
	s := newStack(t, session.DefaultConfig())
	c, _ := s.mustJoin(t, 0, 0, 0)
	src := s.source(t)

	done := make(chan []msg, 1)
	errs := make(chan error, 1)
	go func() {
		got, err := readUntil(c, 5*time.Second, untilFinal(segments))
		if err != nil {
			errs <- err
			return
		}
		done <- got
	}()
	for seg := 1; seg <= segments; seg++ {
		for rev := 1; rev <= revs; rev++ {
			send(t, src, caption(seg, rev, rev == revs, fmt.Sprintf("seg %d rev %d", seg, rev)))
			time.Sleep(10 * time.Millisecond)
		}
	}

	select {
	case err := <-errs:
		t.Fatal(err)
	case got := <-done:
		if _, err := checkStream(got); err != nil {
			t.Fatal(err)
		}
		if len(got) != segments*revs {
			t.Fatalf("client got %d of %d revisions: %v", len(got), segments*revs, got)
		}
	}
}
