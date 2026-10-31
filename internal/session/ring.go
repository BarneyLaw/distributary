package session

// ring is a fixed-capacity FIFO of finals addressed by fseq, a sequence
// number that starts at 1 and only ever increases. The final with fseq f
// lives in buf[f % capacity].
//
// The finals currently held have fseq in [oldest, next): oldest is the
// oldest one still held, next is the fseq the next push will get. The ring
// is empty when oldest == next. Because fseq never resets, a client cursor
// that stores "the last fseq I was given" stays meaningful even after the
// slot it pointed at has been reused.
//
// ring is not safe for concurrent use; Session's mutex protects it.
type ring struct {
	buf 	[]*caption
	oldest	uint64
	next	uint64
}

func newRing(capacity int) ring {
	return ring{
		buf: make([]*caption, capacity),
		oldest: 1,
		next: 1,
	}
}

// len is the number of finals held.
func (r *ring) len() int { 
	return int(r.next - r.oldest)
}

// full reports whether a push would overwrite a final that is still held.
func (r *ring) full() bool {
	return r.len() == len(r.buf)
}

// newestSeq is the fseq of the newest final ever pushed, or 0 if none.
func (r *ring) newestSeq() uint64 { 
	return r.next - 1 
}

// oldestSeq is the fseq of the oldest final still held.
// When the ring is empty, oldestSeq is newestSeq + 1, which is the fseq the next push will get.
func (r *ring) oldestSeq() uint64 {
	return r.oldest
}

// push stores c and returns its fseq. The caller must make room first
// (popOldest) when the ring is full; push panics otherwise, because silently
// overwriting a held final would lose it.
func (r *ring) push(c *caption) uint64 {
	if r.full() {
		panic("sessionL ring.push on full ring")
	}
	fseq := r.next
	r.buf[fseq % uint64(len(r.buf))] = c
	r.next++
	return fseq
}

// popOldest removes and returns the oldest final, or nil if the ring is empty.
func (r *ring) popOldest() *caption {
	if r.len() == 0 {
		return nil
	}
	i := r.oldest % uint64(len(r.buf))
	c := r.buf[i]
	r.buf[i] = nil
	r.oldest++
	return c
}

// peekOldest returns the oldest final without removing it, or nil if empty.
func (r *ring) peekOldest() *caption {
	return r.get(r.oldest)
}

// get returns the final with this fseq, or nil if it is not held (never
// pushed, or already evicted).
func (r *ring) get(fseq uint64) *caption {
	if fseq < r.oldest || fseq >= r.next {
		return nil
	}

	return r.buf[fseq % uint64(len(r.buf))]
}