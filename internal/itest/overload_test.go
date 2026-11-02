package itest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/BarneyLaw/distributary/internal/session"
)

// Implemented with Claude
// A slow reader does not hold up anyone else: during a burst, fast clients
// get every final promptly while one client reads a frame every 50 ms. The
// slow client still gets every final, and the newest revision of the segment
// left active at the end (spec 4.3); older provisionals may be dropped.
func TestSlowReaderIsIsolatedAndStillCorrect(t *testing.T) {
	const fast, segments, revs = 10, 30, 5
	s := newStack(t, session.DefaultConfig())

	slow, _ := s.mustJoin(t, 0, 0, 0)
	slowDone := make(chan error, 1)
	go func() {
		var got []msg
		deadline := time.Now().Add(15 * time.Second)
		for !(untilFinal(segments)(got) && untilRevision(segments+1, revs)(got)) {
			if time.Now().After(deadline) {
				slowDone <- fmt.Errorf("slow client: timed out, got %v", got)
				return
			}
			m, err := read(slow, 5*time.Second)
			if err != nil {
				slowDone <- fmt.Errorf("slow client: %w, got %v", err, got)
				return
			}
			got = append(got, m)
			time.Sleep(50 * time.Millisecond)
		}
		finals, err := checkStream(got)
		if err == nil && !equal(finals, seq(1, segments)) {
			err = fmt.Errorf("slow client finals %v, want 1..%d", finals, segments)
		}
		slowDone <- err
	}()

	fastErrs := make(chan error, fast)
	var wg sync.WaitGroup
	for i := 0; i < fast; i++ {
		c, _ := s.mustJoin(t, 0, 0, 0)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The whole burst at 50 ms per frame would take the slow client
			// over 7 s; a fast client must not be anywhere near that.
			got, err := readUntil(c, 2*time.Second, untilFinal(segments))
			if err == nil {
				var finals []int64
				finals, err = checkStream(got)
				if err == nil && !equal(finals, seq(1, segments)) {
					err = fmt.Errorf("fast client finals %v, want 1..%d", finals, segments)
				}
			}
			if err != nil {
				fastErrs <- fmt.Errorf("fast client: %w", err)
			}
		}()
	}

	src := s.source(t)
	sendSegments(t, src, 1, segments, revs)
	for rev := 1; rev <= revs; rev++ { // segment left active
		send(t, src, caption(segments+1, rev, false, fmt.Sprintf("active rev %d", rev)))
	}

	wg.Wait()
	close(fastErrs)
	for err := range fastErrs {
		t.Error(err)
	}
	if err := <-slowDone; err != nil {
		t.Fatal(err)
	}
}

// Implemented with Claude
// Rapid convergence: a client that stalls through a burst and then resumes
// jumps to the newest state. It must not drain a backlog of every revision
// it missed, so it receives far fewer frames than the source sent.
func TestStalledClientConvergesWithoutBacklog(t *testing.T) {
	const segments, revs = 50, 5
	s := newStack(t, session.DefaultConfig())
	c, _ := s.mustJoin(t, 0, 0, 0)
	src := s.source(t)

	// The client does not read during the burst.
	s.waitAccepted(t, sendSegments(t, src, 1, segments, revs))
	time.Sleep(200 * time.Millisecond)

	got, err := readUntil(c, 5*time.Second, untilFinal(segments))
	if err != nil {
		t.Fatal(err)
	}
	finals, err := checkStream(got)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(finals, seq(1, segments)) {
		t.Fatalf("finals %v, want 1..%d", finals, segments)
	}
	// Every final, plus at most the few frames that were in flight when the
	// client stalled.
	if max := segments + 10; len(got) > max {
		t.Fatalf("stalled client received %d frames to catch up, want at most %d of the %d sent: %v",
			len(got), max, segments*revs, got)
	}
}

// Implemented with Claude
// Bounded state: a client stalled for so long that the finals it still needs
// were evicted is closed with 1013 rather than buffered for. It reconnects,
// recovers what is still retained through HISTORY, and sees no duplicates.
func TestClientBehindRetainedHistoryIsClosedAndRecovers(t *testing.T) {
	const ring, segments = 8, 40
	s := newStack(t, session.Config{MaxFinals: ring})
	c1, _ := s.mustJoin(t, 0, 0, 60)
	src := s.source(t)

	// Stall through the whole burst: the ring only keeps the newest 8 finals.
	s.waitAccepted(t, sendSegments(t, src, 1, segments, 1))

	var first []msg
	var closeErr *websocket.CloseError
	for {
		m, err := read(c1, 5*time.Second)
		if err != nil {
			if !errors.As(err, &closeErr) {
				t.Fatalf("stalled client: %v after %v, want close 1013", err, first)
			}
			break
		}
		first = append(first, m)
	}
	if closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("close code %d, want %d (try again later)", closeErr.Code, websocket.CloseTryAgainLater)
	}
	if _, err := checkStream(first); err != nil {
		t.Fatal(err)
	}

	var lastSeg, lastRev int64
	if n := len(first); n > 0 {
		lastSeg, lastRev = first[n-1].Segment, first[n-1].Revision
	}
	c2, items := s.mustJoin(t, lastSeg, lastRev, 60)
	if len(items) == 0 || len(items) > ring {
		t.Fatalf("HISTORY after falling behind has %d items, want 1..%d", len(items), ring)
	}
	if last := items[len(items)-1]; last.Segment != segments {
		t.Fatalf("HISTORY ends at %v, want final %d", last, segments)
	}
	send(t, src, caption(segments+1, 1, true, "live"))
	live, err := readUntil(c2, 2*time.Second, untilFinal(segments+1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStream(append(append(first, items...), live...)); err != nil {
		t.Fatal(err)
	}
}
