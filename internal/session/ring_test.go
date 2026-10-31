package session

import "testing"

func c(seg int64) *caption { return &caption{segment: seg, revision: 1, final: true} }

func TestRingEmpty(t *testing.T) {
	r := newRing(3)
	if r.len() != 0 || r.full() || r.newestSeq() != 0 || r.oldestSeq() != 1 {
		t.Fatalf("empty ring: len=%d full=%v newest=%d oldest=%d", r.len(), r.full(), r.newestSeq(), r.oldestSeq())
	}
	if r.popOldest() != nil || r.peekOldest() != nil || r.get(0) != nil || r.get(1) != nil {
		t.Fatal("an empty ring holds nothing")
	}
}

func TestRingPushAssignsIncreasingSeq(t *testing.T) {
	r := newRing(3)
	for want := uint64(1); want <= 3; want++ {
		if got := r.push(c(int64(want * 10))); got != want {
			t.Fatalf("push #%d got fseq %d", want, got)
		}
	}
	if !r.full() || r.len() != 3 || r.oldestSeq() != 1 || r.newestSeq() != 3 {
		t.Fatalf("after 3 pushes: len=%d full=%v oldest=%d newest=%d", r.len(), r.full(), r.oldestSeq(), r.newestSeq())
	}
	for f := uint64(1); f <= 3; f++ {
		if got := r.get(f); got == nil || got.segment != int64(f*10) {
			t.Fatalf("get(%d) = %v", f, got)
		}
	}
	if r.get(4) != nil {
		t.Fatal("get beyond newest must be nil")
	}
}

func TestRingWrapsAroundAndKeepsSeqMeaningful(t *testing.T) {
	r := newRing(3)
	for seg := int64(1); seg <= 10; seg++ {
		if r.full() {
			r.popOldest()
		}
		r.push(c(seg))
	}
	// Holds fseq 8, 9, 10 (segments 8, 9, 10) in reused slots.
	if r.oldestSeq() != 8 || r.newestSeq() != 10 || r.len() != 3 {
		t.Fatalf("oldest=%d newest=%d len=%d", r.oldestSeq(), r.newestSeq(), r.len())
	}
	for f := uint64(8); f <= 10; f++ {
		if got := r.get(f); got == nil || got.segment != int64(f) {
			t.Fatalf("get(%d) = %v", f, got)
		}
	}
	// fseq 5 shares a slot with 8, but is gone: get must not return 8's caption.
	if r.get(5) != nil || r.get(7) != nil {
		t.Fatal("evicted fseq must return nil even though its slot was reused")
	}
}

func TestRingPopOldestInOrder(t *testing.T) {
	r := newRing(4)
	for seg := int64(1); seg <= 3; seg++ {
		r.push(c(seg))
	}
	if p := r.peekOldest(); p == nil || p.segment != 1 {
		t.Fatalf("peekOldest = %v", p)
	}
	for want := int64(1); want <= 3; want++ {
		if got := r.popOldest(); got == nil || got.segment != want {
			t.Fatalf("popOldest = %v, want segment %d", got, want)
		}
	}
	if r.len() != 0 || r.popOldest() != nil {
		t.Fatal("ring should be empty after popping everything")
	}
	if r.oldestSeq() != r.newestSeq()+1 {
		t.Fatalf("empty ring: oldest=%d newest=%d", r.oldestSeq(), r.newestSeq())
	}
	if got := r.push(c(4)); got != 4 {
		t.Fatalf("fseq must keep increasing after the ring empties: got %d", got)
	}
}

func TestRingCapacityOne(t *testing.T) {
	r := newRing(1)
	r.push(c(1))
	if !r.full() {
		t.Fatal("capacity-1 ring with one final is full")
	}
	r.popOldest()
	if got := r.push(c(2)); got != 2 || r.get(2).segment != 2 || r.get(1) != nil {
		t.Fatalf("push=%d get(2)=%v get(1)=%v", got, r.get(2), r.get(1))
	}
}

func TestRingPushOnFullPanics(t *testing.T) {
	r := newRing(1)
	r.push(c(1))
	defer func() {
		if recover() == nil {
			t.Fatal("push on a full ring must panic rather than silently drop a final")
		}
	}()
	r.push(c(2))
}
