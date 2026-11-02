package itest

import (
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/BarneyLaw/distributary/internal/session"
)

// Implemented with Claude
// Late join: HISTORY first (finals only, oldest first), then live updates,
// with no gap and no overlap between them.
func TestLateJoinGetsHistoryThenLive(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	src := s.source(t)
	n := sendSegments(t, src, 1, 10, 3)
	send(t, src, caption(11, 1, false, "in progress"))
	s.waitAccepted(t, n+1)

	c, items := s.mustJoin(t, 0, 0, 60)
	if len(items) != 10 {
		t.Fatalf("HISTORY has %d items %v, want finals 1..10", len(items), items)
	}
	for i, m := range items {
		if !m.Final || m.Segment != int64(i+1) {
			t.Fatalf("HISTORY item %d is %v, want final %d", i, m, i+1)
		}
	}

	send(t, src, caption(11, 2, false, "in progress more"))
	send(t, src, caption(11, 3, true, "done"))
	send(t, src, caption(12, 1, true, "next"))
	live, err := readUntil(c, 2*time.Second, untilFinal(12))
	if err != nil {
		t.Fatal(err)
	}
	finals, err := checkStream(append(items, live...))
	if err != nil {
		t.Fatal(err)
	}
	if !equal(finals, seq(1, 12)) {
		t.Fatalf("finals %v, want 1..12", finals)
	}
}

// Implemented with Claude
// HISTORY honours history_seconds, measured back from the newest source_ts,
// and history_seconds=0 gets no history, now or later.
func TestHistorySecondsWindow(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	src := s.source(t)
	s.waitAccepted(t, sendSegments(t, src, 1, 40, 1)) // segment k at k*0.5 s

	// newest is segment 40 at 20 s; 5.25 s back is 14.75 s, so 30 (15 s) is in
	// and 29 (14.5 s) is out.
	_, items := s.mustJoin(t, 0, 0, 5.25)
	var segs []int64
	for _, m := range items {
		segs = append(segs, m.Segment)
	}
	if !equal(segs, seq(30, 40)) {
		t.Fatalf("history_seconds=5.25 gave segments %v, want 30..40", segs)
	}

	c, items := s.mustJoin(t, 0, 0, 0)
	if len(items) != 0 {
		t.Fatalf("history_seconds=0 gave %v, want empty HISTORY", items)
	}
	send(t, src, caption(41, 1, true, "live"))
	live, err := readUntil(c, 2*time.Second, untilFinal(41))
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatalf("history_seconds=0 client got %v, want only the live final 41", live)
	}
}

// Implemented with Claude
// Reconnect: the client reports its newest final and gets exactly what it
// missed. Across both connections, every final is seen exactly once.
func TestReconnectRecoversExactlyWhatWasMissed(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	c1, _ := s.mustJoin(t, 0, 0, 30)
	src := s.source(t)

	sendSegments(t, src, 1, 5, 2)
	first, err := readUntil(c1, 2*time.Second, untilFinal(5))
	if err != nil {
		t.Fatal(err)
	}
	c1.Close()

	before := s.src.Stats().Accepted
	s.waitAccepted(t, before+sendSegments(t, src, 6, 15, 2))

	last := first[len(first)-1]
	c2, items := s.mustJoin(t, last.Segment, last.Revision, 30)
	if len(items) != 10 || items[0].Segment != 6 {
		t.Fatalf("HISTORY after reconnect is %v, want finals 6..15", items)
	}
	sendSegments(t, src, 16, 18, 2)
	live, err := readUntil(c2, 2*time.Second, untilFinal(18))
	if err != nil {
		t.Fatal(err)
	}

	all := append(append(first, items...), live...)
	finals, err := checkStream(all)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(finals, seq(1, 18)) {
		t.Fatalf("finals across reconnect %v, want 1..18", finals)
	}
}

// Implemented with Claude
// Reconnecting in the middle of a segment: the client may already hold that
// segment's provisional, so it must not get it again; it continues from the
// next revision or the final.
func TestReconnectMidSegmentDoesNotRepeatProvisional(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	c1, _ := s.mustJoin(t, 0, 0, 30)
	src := s.source(t)

	send(t, src, caption(1, 1, true, "one"))
	send(t, src, caption(2, 1, false, "tw"))
	send(t, src, caption(2, 2, false, "two"))
	first, err := readUntil(c1, 2*time.Second, untilRevision(2, 2))
	if err != nil {
		t.Fatal(err)
	}
	c1.Close()

	c2, items := s.mustJoin(t, 1, 1, 30)
	if len(items) != 0 {
		t.Fatalf("HISTORY %v, want empty: segment 1 is held and segment 2 is not final", items)
	}
	send(t, src, caption(2, 3, false, "two more"))
	send(t, src, caption(2, 4, true, "two more words"))
	live, err := readUntil(c2, 2*time.Second, untilFinal(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStream(append(first, live...)); err != nil { // a repeated 2.2 fails here
		t.Fatal(err)
	}
}

// Implemented with Claude
// Many clients drop at the same moment and reconnect together while the
// source keeps streaming. Each ends up with every final exactly once.
func TestSimultaneousReconnects(t *testing.T) {
	const clients, dropAt, segments = 20, 15, 60
	s := newStack(t, session.DefaultConfig())

	var dropped, rejoin sync.WaitGroup
	dropped.Add(clients)
	rejoin.Add(1)
	errs := make(chan error, clients)
	var done sync.WaitGroup
	for i := 0; i < clients; i++ {
		c1, _ := s.mustJoin(t, 0, 0, 60)
		done.Add(1)
		go func() {
			defer done.Done()
			first, err := readUntil(c1, 5*time.Second, untilFinal(dropAt))
			c1.Close()
			dropped.Done()
			if err != nil {
				errs <- err
				return
			}
			rejoin.Wait()

			last := first[len(first)-1]
			for !last.Final { // report the newest final held, not a provisional
				first = first[:len(first)-1]
				last = first[len(first)-1]
			}
			c2, items, err := s.join(last.Segment, last.Revision, 60)
			if err != nil {
				errs <- err
				return
			}
			defer c2.Close()
			all := append(first, items...)
			if !untilFinal(segments)(all) {
				live, err := readUntil(c2, 5*time.Second, untilFinal(segments))
				if err != nil {
					errs <- err
					return
				}
				all = append(all, live...)
			}
			finals, err := checkStream(all)
			if err == nil && !equal(finals, seq(1, segments)) {
				err = errorf("finals across reconnect %v, want 1..%d", finals, segments)
			}
			if err != nil {
				errs <- err
			}
		}()
	}

	src := s.source(t)
	srcErr := make(chan error, 1)
	go func() {
		for seg := 1; seg <= segments; seg++ {
			for rev := 1; rev <= 3; rev++ {
				if err := src.WriteMessage(websocket.TextMessage, []byte(caption(seg, rev, rev == 3, "x"))); err != nil {
					srcErr <- err
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
		srcErr <- nil
	}()

	dropped.Wait()
	rejoin.Done() // everyone reconnects at once
	done.Wait()
	if err := <-srcErr; err != nil {
		t.Fatal(err)
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
