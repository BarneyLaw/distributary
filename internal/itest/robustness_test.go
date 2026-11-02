package itest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/BarneyLaw/distributary/internal/session"
)

// Implemented with Claude
// A new source replaces the old one; a source leaving does not disconnect
// clients, and the next source carries the session on.
func TestSourceReplacedAndReconnected(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	c, _ := s.mustJoin(t, 0, 0, 0)

	a := s.source(t)
	send(t, a, caption(1, 1, true, "from a"))
	s.waitAccepted(t, 1)

	b := s.source(t) // takes over; the server closes a
	send(t, b, caption(2, 1, true, "from b"))
	a.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := a.ReadMessage(); err == nil {
		t.Fatal("old source still open after takeover")
	}

	b.Close()
	time.Sleep(50 * time.Millisecond)
	d := s.source(t)
	send(t, d, caption(3, 1, true, "from d"))

	got, err := readUntil(c, 2*time.Second, untilFinal(3))
	if err != nil {
		t.Fatal(err)
	}
	finals, err := checkStream(got)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(finals, []int64{1, 2, 3}) {
		t.Fatalf("finals %v, want [1 2 3]", finals)
	}
}

// Implemented with Claude
// Bad or missing hellos are closed with 1008 and do not disturb a client
// that is already connected.
func TestBadHellosAreRejectedWithoutAffectingClients(t *testing.T) {
	s := newStack(t, session.DefaultConfig())
	c, _ := s.mustJoin(t, 0, 0, 0)
	src := s.source(t)

	for name, hello := range map[string]string{
		"not json":        `hello`,
		"caption first":   caption(1, 1, true, "x"),
		"history":         `{"type":"HISTORY","items":[]}`,
		"no hello at all": "",
	} {
		bad, err := s.dial()
		if err != nil {
			t.Fatal(err)
		}
		if hello != "" {
			send(t, bad, hello)
		}
		bad.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, err = bad.ReadMessage()
		var ce *websocket.CloseError
		if !errors.As(err, &ce) || ce.Code != websocket.ClosePolicyViolation {
			t.Errorf("%s: got %v, want close 1008", name, err)
		}
		bad.Close()
	}

	send(t, src, caption(1, 1, true, "still here"))
	if _, err := readUntil(c, 2*time.Second, untilFinal(1)); err != nil {
		t.Fatal(err)
	}
}

// Implemented with Claude
// Clients joining and leaving all the time while the source streams: no
// race or panic (run with -race), and a client that stays gets every final.
func TestClientChurnDuringStream(t *testing.T) {
	const segments = 100
	s := newStack(t, session.DefaultConfig())
	stay, _ := s.mustJoin(t, 0, 0, 0)

	stop := make(chan struct{})
	var joins atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, _, err := s.join(0, 0, 60)
				if err != nil {
					continue
				}
				joins.Add(1)
				read(c, 20*time.Millisecond)
				c.Close()
			}
		}()
	}

	got := make(chan error, 1)
	go func() {
		ms, err := readUntil(stay, 10*time.Second, untilFinal(segments))
		if err == nil {
			var finals []int64
			finals, err = checkStream(ms)
			if err == nil && !equal(finals, seq(1, segments)) {
				err = errorf("staying client finals %v, want 1..%d", finals, segments)
			}
		}
		got <- err
	}()

	src := s.source(t)
	for seg := 1; seg <= segments; seg++ {
		send(t, src, caption(seg, 1, false, "p"))
		send(t, src, caption(seg, 2, true, "f"))
		time.Sleep(time.Millisecond)
	}
	err := <-got
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if joins.Load() < 10 {
		t.Fatalf("only %d churn joins happened, test did not exercise churn", joins.Load())
	}
}
