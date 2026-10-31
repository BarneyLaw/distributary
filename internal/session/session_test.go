package session_test

// These tests are the specification for package session. They are grouped by
// the order to build things in:
//
//	Step 1  Apply and its verdicts               go test -run 'Apply|Wake'
//	Step 2  the ring, eviction and Join/HISTORY  go test -run 'Join|Evict|Bounded'
//	Step 3  Pick and the cursor                  go test -run 'Pick|Rejoin'
//	Step 4  everything at once, concurrently     go test -race -run Concurrent
//
// They only use the exported API, so the internals are yours to design.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/BarneyLaw/distributary/internal/session"
)

// ---------------------------------------------------------------- helpers

// big is a config that never evicts in a short test.
var big = session.Config{Retention: time.Hour, MaxFinals: 100_000, MaxActive: 16}

func upd(seg, rev int64, ts float64, final bool) *protocol.CaptionUpdate {
	return &protocol.CaptionUpdate{
		Type:     protocol.TypeCaptionUpdate,
		Session:  "L1",
		Segment:  seg,
		Revision: rev,
		SourceTS: ts,
		Final:    final,
		Text:     fmt.Sprintf("seg %d rev %d", seg, rev),
	}
}

func prov(seg, rev int64, ts float64) *protocol.CaptionUpdate  { return upd(seg, rev, ts, false) }
func final(seg, rev int64, ts float64) *protocol.CaptionUpdate { return upd(seg, rev, ts, true) }

func hello(lastSegment int64, historySeconds float64) *protocol.ClientHello {
	h := &protocol.ClientHello{Type: protocol.TypeClientHello, Session: "L1",
		LastSegment: lastSegment, HistorySeconds: historySeconds}
	if lastSegment > 0 {
		h.LastRevision = 3
	}
	return h
}

// item is a decoded frame: what the client would see.
type item struct {
	Seg, Rev int64
	Final    bool
}

func (i item) String() string {
	if i.Final {
		return fmt.Sprintf("%d.%dF", i.Seg, i.Rev)
	}
	return fmt.Sprintf("%d.%d", i.Seg, i.Rev)
}

func F(seg, rev int64) item { return item{seg, rev, true} }
func P(seg, rev int64) item { return item{seg, rev, false} }

type wireCaption struct {
	Type     string  `json:"type"`
	Segment  int64   `json:"segment"`
	Revision int64   `json:"revision"`
	SourceTS float64 `json:"source_ts"`
	Final    bool    `json:"final"`
	Text     string  `json:"text"`
}

func decodeCaption(t *testing.T, raw []byte) item {
	t.Helper()
	var c wireCaption
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("frame is not JSON: %v: %s", err, raw)
	}
	if c.Type != protocol.TypeCaptionUpdate {
		t.Fatalf("frame type %q, want CAPTION_UPDATE: %s", c.Type, raw)
	}
	if want := fmt.Sprintf("seg %d rev %d", c.Segment, c.Revision); c.Text != want {
		t.Fatalf("frame text %q, want %q (frames must be the Encode() bytes of that update)", c.Text, want)
	}
	return item{c.Segment, c.Revision, c.Final}
}

func decodeFrames(t *testing.T, frames [][]byte) []item {
	t.Helper()
	out := []item{}
	for _, f := range frames {
		out = append(out, decodeCaption(t, f))
	}
	return out
}

func decodeHistory(t *testing.T, raw []byte) []item {
	t.Helper()
	if raw == nil {
		t.Fatal("Join returned no HISTORY frame")
	}
	var h struct {
		Type  string            `json:"type"`
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("HISTORY is not JSON: %v: %s", err, raw)
	}
	if h.Type != protocol.TypeHistory || h.Items == nil {
		t.Fatalf("want {\"type\":\"HISTORY\",\"items\":[...]}, got %s", raw)
	}
	out := []item{}
	for _, it := range h.Items {
		c := decodeCaption(t, it)
		if !c.Final {
			t.Fatalf("HISTORY contains a provisional %v; it must hold finals only", c)
		}
		out = append(out, c)
	}
	return out
}

func equal(a, b []item) bool {
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

func wantItems(t *testing.T, what string, got, want []item) {
	t.Helper()
	if !equal(got, want) {
		t.Fatalf("%s:\n got  %v\n want %v", what, got, want)
	}
}

func mustApply(t *testing.T, s *session.Session, want session.Verdict, updates ...*protocol.CaptionUpdate) {
	t.Helper()
	for _, u := range updates {
		if got := s.Apply(u); got != want {
			t.Fatalf("Apply(%s) = %v, want %v", item{u.Segment, u.Revision, u.Final}, got, want)
		}
	}
}

// pick calls Pick and decodes the frames, failing on behind.
func pick(t *testing.T, s *session.Session, cur *session.Cursor, max int, provisionals bool) []item {
	t.Helper()
	frames, _, behind := s.Pick(cur, max, provisionals)
	if behind {
		t.Fatal("Pick reported behind unexpectedly")
	}
	return decodeFrames(t, frames)
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------- step 1: Apply

func TestApplyVerdicts(t *testing.T) {
	s := session.New(big)
	steps := []struct {
		name string
		u    *protocol.CaptionUpdate
		want session.Verdict
	}{
		{"first revision of a segment", prov(1, 1, 100.0), session.Accepted},
		{"same revision again", prov(1, 1, 100.1), session.Duplicate},
		{"newer revision", prov(1, 2, 100.2), session.Accepted},
		{"older revision after a newer one", prov(1, 1, 100.3), session.Stale},
		{"final", final(1, 3, 100.4), session.Accepted},
		{"provisional after final", prov(1, 4, 100.5), session.AlreadyFinal},
		{"higher final after final", final(1, 4, 100.6), session.AlreadyFinal},
		{"the same final again", final(1, 3, 100.7), session.AlreadyFinal},
		{"single-revision segment (short sentence)", final(2, 1, 101.0), session.Accepted},
		{"segment numbers may skip", prov(4, 1, 102.0), session.Accepted},
		{"a lower segment while another is active", prov(3, 1, 102.1), session.Accepted},
		{"more than three revisions", prov(4, 7, 102.2), session.Accepted},
		{"final after many revisions", final(4, 8, 102.3), session.Accepted},
	}
	for _, st := range steps {
		if got := s.Apply(st.u); got != st.want {
			t.Fatalf("%s: Apply(%s) = %v, want %v", st.name, item{st.u.Segment, st.u.Revision, st.u.Final}, got, st.want)
		}
	}
}

func TestApplyTooOldOnceFinalEvicted(t *testing.T) {
	s := session.New(session.Config{Retention: time.Hour, MaxFinals: 2, MaxActive: 16})
	mustApply(t, s, session.Accepted, final(1, 1, 100), final(2, 1, 101), final(3, 1, 102)) // evicts segment 1

	mustApply(t, s, session.TooOld, prov(1, 9, 103), final(1, 9, 103))
	mustApply(t, s, session.AlreadyFinal, prov(2, 9, 103)) // segment 2 is still in the ring
	mustApply(t, s, session.Accepted, prov(4, 1, 104))
}

func TestApplyActiveEvictionKeepsTheFinal(t *testing.T) {
	// Evicting a provisional-only segment must not make its final TooOld.
	s := session.New(session.Config{Retention: time.Hour, MaxFinals: 100, MaxActive: 2})
	_, cur := s.Join(hello(0, 60))
	mustApply(t, s, session.Accepted, prov(1, 1, 100), prov(2, 1, 101), prov(3, 1, 102)) // evicts 1's provisional

	wantItems(t, "provisionals kept after MaxActive eviction", pick(t, s, &cur, 10, true), []item{P(2, 1), P(3, 1)})

	mustApply(t, s, session.Accepted, final(1, 2, 103))
	wantItems(t, "final of an evicted provisional segment", pick(t, s, &cur, 10, true), []item{F(1, 2)})
}

func TestWakeClosesOnAcceptedUpdateOnly(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))

	frames, w1, _ := s.Pick(&cur, 10, true)
	if w1 == nil {
		t.Fatal("Pick must return a wake channel")
	}
	if len(frames) != 0 || isClosed(w1) {
		t.Fatal("nothing has happened yet: no frames, wake still open")
	}

	mustApply(t, s, session.Accepted, prov(1, 2, 100))
	if !isClosed(w1) {
		t.Fatal("an Accepted Apply must close the wake channel the client is holding")
	}

	_, w2, _ := s.Pick(&cur, 10, true)
	if w2 == nil || isClosed(w2) {
		t.Fatal("after picking, the next wake channel must be open")
	}
	mustApply(t, s, session.Duplicate, prov(1, 2, 100.1))
	mustApply(t, s, session.Stale, prov(1, 1, 100.2))
	if isClosed(w2) {
		t.Fatal("a rejected Apply must not wake clients")
	}
	mustApply(t, s, session.Accepted, final(1, 3, 100.3))
	if !isClosed(w2) {
		t.Fatal("an Accepted final must wake clients")
	}
}

// ---------------------------------------------------------------- step 2: ring, eviction, Join

// feed applies segments from..to, each as revisions 1 and 2 (provisional)
// and 3 (final), with source_ts = base + seg*step.
func feed(t *testing.T, s *session.Session, from, to int64, base, step float64) {
	t.Helper()
	for seg := from; seg <= to; seg++ {
		ts := base + float64(seg)*step
		mustApply(t, s, session.Accepted, prov(seg, 1, ts), prov(seg, 2, ts), final(seg, 3, ts))
	}
}

func finals(from, to int64) []item {
	out := []item{}
	for seg := from; seg <= to; seg++ {
		out = append(out, F(seg, 3))
	}
	return out
}

func TestJoinEmptySession(t *testing.T) {
	s := session.New(big)
	history, cur := s.Join(hello(0, 30))
	wantItems(t, "HISTORY of an empty session", decodeHistory(t, history), []item{})
	wantItems(t, "Pick on an empty session", pick(t, s, &cur, 10, true), []item{})

	feed(t, s, 1, 2, 100, 1)
	wantItems(t, "live finals after joining an empty session", pick(t, s, &cur, 10, true), finals(1, 2))
}

func TestJoinHistoryHoldsFinalsOnlyOldestFirst(t *testing.T) {
	s := session.New(big)
	feed(t, s, 1, 5, 100, 1)
	mustApply(t, s, session.Accepted, prov(6, 1, 106)) // in progress: never in HISTORY

	history, _ := s.Join(hello(0, 60))
	wantItems(t, "HISTORY for a new client", decodeHistory(t, history), finals(1, 5))
}

func TestJoinHistoryRespectsLastSegment(t *testing.T) {
	s := session.New(big)
	feed(t, s, 1, 5, 100, 1)

	history, _ := s.Join(hello(3, 60))
	wantItems(t, "HISTORY after last_segment=3", decodeHistory(t, history), finals(4, 5))

	history, _ = s.Join(hello(5, 60))
	wantItems(t, "HISTORY when the client is up to date", decodeHistory(t, history), []item{})
}

func TestJoinHistoryRespectsHistorySeconds(t *testing.T) {
	s := session.New(big)
	feed(t, s, 1, 5, 90, 10) // finals at source_ts 100, 110, 120, 130, 140

	history, _ := s.Join(hello(0, 25)) // window starts at 140 - 25 = 115
	wantItems(t, "HISTORY with history_seconds=25", decodeHistory(t, history), finals(3, 5))

	history, _ = s.Join(hello(0, 0))
	wantItems(t, "HISTORY with history_seconds=0", decodeHistory(t, history), []item{})

	// The window is measured from the newest source_ts seen, provisionals included.
	mustApply(t, s, session.Accepted, prov(6, 1, 150)) // window now starts at 150 - 25 = 125
	history, _ = s.Join(hello(0, 25))
	wantItems(t, "HISTORY after a newer provisional", decodeHistory(t, history), finals(4, 5))
}

func TestJoinCursorStartsAfterNewestFinalNotAfterHistory(t *testing.T) {
	s := session.New(big)
	feed(t, s, 1, 5, 90, 10) // finals at 100 ... 140

	history, cur := s.Join(hello(0, 25))
	wantItems(t, "HISTORY", decodeHistory(t, history), finals(3, 5))
	wantItems(t, "finals outside the requested window must not leak through Pick", pick(t, s, &cur, 10, true), []item{})

	feed(t, s, 6, 6, 90, 10)
	wantItems(t, "live after HISTORY", pick(t, s, &cur, 10, true), finals(6, 6))
}

func TestEvictByCount(t *testing.T) {
	s := session.New(session.Config{Retention: time.Hour, MaxFinals: 3, MaxActive: 16})
	feed(t, s, 1, 5, 100, 1)
	history, _ := s.Join(hello(0, 60))
	wantItems(t, "ring holds the newest MaxFinals finals", decodeHistory(t, history), finals(3, 5))
}

func TestEvictByAge(t *testing.T) {
	s := session.New(session.Config{Retention: 10 * time.Second, MaxFinals: 100, MaxActive: 16})
	mustApply(t, s, session.Accepted,
		final(1, 1, 100), final(2, 1, 105), final(3, 1, 112), final(4, 1, 120)) // keeps ts > 110

	history, _ := s.Join(hello(0, 60))
	wantItems(t, "finals older than newest source_ts - Retention are evicted", decodeHistory(t, history),
		[]item{F(3, 1), F(4, 1)})
	mustApply(t, s, session.TooOld, prov(2, 5, 121))
}

func TestBoundedMemory(t *testing.T) {
	const maxFinals = 64
	s := session.New(session.Config{Retention: time.Hour, MaxFinals: maxFinals, MaxActive: 4})
	for seg := int64(1); seg <= 50_000; seg++ {
		ts := 1000 + float64(seg)*0.0001
		mustApply(t, s, session.Accepted, prov(seg, 1, ts), final(seg, 2, ts))
	}

	history, cur := s.Join(hello(0, 60))
	if n := len(decodeHistory(t, history)); n != maxFinals {
		t.Fatalf("HISTORY has %d finals; the ring must hold exactly MaxFinals=%d", n, maxFinals)
	}

	for seg := int64(50_001); seg <= 50_010; seg++ {
		mustApply(t, s, session.Accepted, prov(seg, 1, 1006)) // 10 unfinished segments
	}
	wantItems(t, "only the MaxActive=4 highest unfinished segments are tracked", pick(t, s, &cur, 100, true),
		[]item{P(50_007, 1), P(50_008, 1), P(50_009, 1), P(50_010, 1)})
}

// ---------------------------------------------------------------- step 3: Pick and the cursor

func TestPickFinalsBeforeProvisionals(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))
	mustApply(t, s, session.Accepted, final(1, 1, 100), prov(3, 1, 101), final(2, 1, 102))

	wantItems(t, "finals first in order, then provisionals", pick(t, s, &cur, 10, true),
		[]item{F(1, 1), F(2, 1), P(3, 1)})
}

func TestPickRespectsMax(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))
	mustApply(t, s, session.Accepted, final(1, 1, 100), final(2, 1, 101), prov(3, 1, 102))

	wantItems(t, "max=0", pick(t, s, &cur, 0, true), []item{})
	wantItems(t, "max=-1", pick(t, s, &cur, -1, true), []item{})
	wantItems(t, "max=1 (1st)", pick(t, s, &cur, 1, true), []item{F(1, 1)})
	wantItems(t, "max=1 (2nd)", pick(t, s, &cur, 1, true), []item{F(2, 1)})
	wantItems(t, "max=1 (3rd)", pick(t, s, &cur, 1, true), []item{P(3, 1)})
	wantItems(t, "max=1 (nothing left)", pick(t, s, &cur, 1, true), []item{})
}

func TestPickNeverRepeats(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))
	mustApply(t, s, session.Accepted, prov(1, 1, 100))
	wantItems(t, "first pick", pick(t, s, &cur, 10, true), []item{P(1, 1)})
	wantItems(t, "second pick, nothing new", pick(t, s, &cur, 10, true), []item{})

	mustApply(t, s, session.Accepted, final(1, 2, 101))
	wantItems(t, "the final", pick(t, s, &cur, 10, true), []item{F(1, 2)})
	wantItems(t, "after the final", pick(t, s, &cur, 10, true), []item{})
}

func TestPickFastClientSeesEveryRevision(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))
	var got []item
	for _, u := range []*protocol.CaptionUpdate{prov(1, 1, 100), prov(1, 2, 100.2), final(1, 3, 100.4)} {
		mustApply(t, s, session.Accepted, u)
		got = append(got, pick(t, s, &cur, 10, true)...)
	}
	wantItems(t, "a client that keeps up gets every revision", got, []item{P(1, 1), P(1, 2), F(1, 3)})
}

func TestPickSlowClientGetsOnlyNewestProvisional(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))
	mustApply(t, s, session.Accepted, prov(1, 1, 100), prov(1, 2, 100.2), prov(1, 3, 100.4))
	wantItems(t, "superseded provisionals are skipped", pick(t, s, &cur, 10, true), []item{P(1, 3)})

	mustApply(t, s, session.Accepted, prov(1, 4, 100.6), final(1, 5, 100.8))
	wantItems(t, "a provisional superseded by the final is skipped", pick(t, s, &cur, 10, true), []item{F(1, 5)})
}

func TestPickDegradedModeSendsFinalsOnly(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))
	mustApply(t, s, session.Accepted, prov(1, 1, 100), prov(1, 2, 100.2))
	wantItems(t, "degraded: no provisionals", pick(t, s, &cur, 10, false), []item{})

	mustApply(t, s, session.Accepted, final(1, 3, 100.4), prov(2, 1, 101))
	wantItems(t, "degraded: the final still goes", pick(t, s, &cur, 10, false), []item{F(1, 3)})
	wantItems(t, "back to normal: the provisional skipped in degraded mode is still pending",
		pick(t, s, &cur, 10, true), []item{P(2, 1)})
}

func TestPickSkipsFinalsTheClientAlreadyHas(t *testing.T) {
	// A client may report a last_segment we have not reached yet, for example
	// after a server restart. It must never get those finals again.
	s := session.New(big)
	feed(t, s, 1, 3, 100, 1)
	_, cur := s.Join(hello(5, 60))
	feed(t, s, 4, 6, 100, 1)
	wantItems(t, "finals at or below last_segment are skipped", pick(t, s, &cur, 10, true), finals(6, 6))
}

func TestPickBehindWhenNeededFinalWasEvicted(t *testing.T) {
	s := session.New(session.Config{Retention: time.Hour, MaxFinals: 3, MaxActive: 16})
	_, slow := s.Join(hello(0, 30))
	feed(t, s, 1, 5, 100, 1) // finals 1 and 2 leave the ring before "slow" reads them

	frames, _, behind := s.Pick(&slow, 10, true)
	if !behind || len(frames) != 0 {
		t.Fatalf("want behind=true and no frames, got behind=%v and %d frames", behind, len(frames))
	}

	_, current := s.Join(hello(5, 30))
	feed(t, s, 6, 8, 100, 1) // evicts 3, 4 and 5, which "current" never needed
	wantItems(t, "a client that keeps up is never behind", pick(t, s, &current, 10, true), finals(6, 8))
}

func TestRejoinResumesWithoutGapOrDuplicate(t *testing.T) {
	s := session.New(big)
	_, cur := s.Join(hello(0, 30))
	feed(t, s, 1, 3, 100, 1)
	got := pick(t, s, &cur, 10, false)

	// The client disconnects; the lecture goes on.
	feed(t, s, 4, 6, 100, 1)

	history, cur := s.Join(hello(3, 30))
	got = append(got, decodeHistory(t, history)...)
	feed(t, s, 7, 7, 100, 1)
	got = append(got, pick(t, s, &cur, 10, false)...)

	wantItems(t, "finals across a reconnect", got, finals(1, 7))
}

func TestRejoinDoesNotResendProvisionalItMayHaveSeen(t *testing.T) {
	s := session.New(big)
	_, first := s.Join(hello(0, 30))
	mustApply(t, s, session.Accepted, final(1, 1, 100), prov(2, 1, 101))
	wantItems(t, "before disconnect", pick(t, s, &first, 10, true), []item{F(1, 1), P(2, 1)})

	_, second := s.Join(hello(1, 30))
	wantItems(t, "right after reconnect: 2.1 may be a duplicate, so it is not sent", pick(t, s, &second, 10, true), []item{})

	mustApply(t, s, session.Accepted, prov(2, 2, 101.2))
	wantItems(t, "the next revision goes out", pick(t, s, &second, 10, true), []item{P(2, 2)})
}

// ---------------------------------------------------------------- step 4: concurrency

// TestConcurrentClientsSeeEveryFinalOnceInOrder runs one source and many
// clients at once. Clients pick with random credit, randomly switch degraded
// mode on and off, and randomly reconnect. Every client must end up with every
// final exactly once, in order, and must never see a provisional go backwards
// or arrive after its segment's final. Run it with -race.
func TestConcurrentClientsSeeEveryFinalOnceInOrder(t *testing.T) {
	const (
		segments = 2000
		clients  = 40
	)
	s := session.New(big)
	done := make(chan struct{})

	var wg sync.WaitGroup
	// Runs even if the source loop below fails, so no client goroutine
	// outlives the test.
	defer func() {
		close(done)
		wg.Wait()
	}()
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			var got []item
			lastFinal := int64(0)
			highestRev := map[int64]int64{}
			isFinal := map[int64]bool{}

			record := func(it item) bool {
				if it.Final {
					if it.Seg != lastFinal+1 {
						t.Errorf("client %d: final %v after final %d: finals must be complete and in order", id, it, lastFinal)
						return false
					}
					lastFinal = it.Seg
					isFinal[it.Seg] = true
				} else {
					if isFinal[it.Seg] {
						t.Errorf("client %d: provisional %v after its segment's final", id, it)
						return false
					}
					if it.Rev <= highestRev[it.Seg] {
						t.Errorf("client %d: provisional %v is not newer than revision %d it already has", id, it, highestRev[it.Seg])
						return false
					}
					highestRev[it.Seg] = it.Rev
				}
				got = append(got, it)
				return true
			}

			join := func() session.Cursor {
				history, cur := s.Join(hello(lastFinal, 60))
				for _, it := range decodeHistory(t, history) {
					if !record(it) {
						return cur
					}
				}
				return cur
			}

			cur := join()
			for {
				if rng.Intn(100) == 0 {
					cur = join() // reconnect
				}
				provisionals := rng.Intn(4) != 0
				frames, wake, behind := s.Pick(&cur, 1+rng.Intn(8), provisionals)
				if behind {
					t.Errorf("client %d: behind, but the ring is large enough to hold everything", id)
					return
				}
				for _, it := range decodeFrames(t, frames) {
					if !record(it) {
						return
					}
				}
				if len(frames) > 0 {
					continue
				}
				select {
				case <-wake:
				case <-done:
					// The source has finished. If updates were waiting for us
					// while our wake channel stayed open, a wakeup was lost.
					more, _, _ := s.Pick(&cur, 1<<20, provisionals)
					if len(more) > 0 && !isClosed(wake) {
						t.Errorf("client %d: %d frames were pending but its wake channel was never closed", id, len(more))
					}
					for _, it := range decodeFrames(t, more) {
						record(it)
					}
					if lastFinal != segments {
						t.Errorf("client %d: ended with finals up to %d, want %d", id, lastFinal, segments)
					}
					return
				case <-time.After(10 * time.Second):
					t.Errorf("client %d: stuck waiting for a wakeup", id)
					return
				}
			}
		}(c)
	}

	for seg := int64(1); seg <= segments; seg++ {
		ts := 1000 + float64(seg)*0.001
		mustApply(t, s, session.Accepted, prov(seg, 1, ts), prov(seg, 2, ts), final(seg, 3, ts))
	}
}
