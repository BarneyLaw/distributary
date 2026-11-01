package session

import (
	"github.com/BarneyLaw/distributary/internal/protocol"
)

// Apply decides whether to keep one CAPTION_UPDATE from the source, which
// protocol.DecodeSourceMessage has already validated. Rules, in this order:
//
//  1. segment already final                              -> AlreadyFinal
//  2. segment <= highest segment whose final was evicted -> TooOld
//  3. revision == the stored revision of the segment     -> Duplicate
//     revision <  the stored revision of the segment     -> Stale
//  4. otherwise Accepted:
//     final       -> appended to the ring with the next fseq; the segment's
//     provisional is dropped; finals older than newest source_ts minus
//     Retention, or beyond MaxFinals, are evicted (raising the floor)
//     provisional -> replaces the segment's stored provisional; if a new
//     segment would exceed MaxActive, the lowest active segment's provisional
//     is evicted (this does NOT raise the floor: its final may still arrive
//     and must be accepted)
//
// On Accepted, every channel previously returned by Pick is closed.
//
// Apply does a map lookup, at most a few evictions and one channel close.
// It never waits for a client, which is why a slow client can never slow
// down the source (the naive server waited for every client on every caption).
func (s *Session) Apply(u *protocol.CaptionUpdate) Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. If the segment is already final, report AlreadyFinal.
	// This is the only rule that does not depend on the source_ts of the update.
	if _, ok := s.finalized[u.Segment]; ok {
		return AlreadyFinal
	}

	// 2. if the segment is at or below the highest segment
	// whose final has been evicted from the ring, report TooOld.
	if u.Segment <= s.floor {
		return TooOld
	}

	// Rule 3: within a segment, only a strictly higher revision is news.
	// This is what stops an older revision from ever overwriting a newer one.
	if stored, ok := s.active[u.Segment]; ok {
		switch {
		case u.Revision == stored.revision:
			return Duplicate
		case u.Revision < stored.revision:
			return Stale
		}
	}

	// 4. Accepted: store the update, evict any old finals, and wake all clients.
	c := newCaption(u)
	if c.sourceTS > s.newestTS {
		s.newestTS = c.sourceTS
	}

	if c.final {
		s.storeFinal(c)
	} else {
		s.storeProvisional(c)
	}
	s.wakeAll()
	return Accepted
}

// storeFinal appends a final to the ring and enforces both bounds on it.
func (s *Session) storeFinal(c *caption) {
	// segment is complete, no more provisionals are welcome. Drop any provisional for this segment.
	delete(s.active, c.segment)

	// Bound by count, make room for the new final by evicting the oldest one, if any.
	if s.finals.full() {
		s.evictOldestFinal()
	}
	s.finalized[c.segment] = s.finals.push(c)

	// Bound by time, evict any final older than newest source_ts minus Retention.
	// This is measure on the source's clock, not the server's. Newest final always kept.
	cutoff := s.newestTS - s.cfg.Retention.Seconds()
	for s.finals.len() > 1 && s.finals.peekOldest().sourceTS < cutoff {
		s.evictOldestFinal()
	}
}

// evictOldestFinal removes the oldest final from the ring and raises the
// floor, so that later updates for that segment are rejected as TooOld.
func (s *Session) evictOldestFinal() {
	c := s.finals.popOldest()
	if c == nil {
		return
	}
	delete(s.finalized, c.segment)
	if c.segment > s.floor {
		s.floor = c.segment
	}
}

// storeProvisional keeps only the newest provisional per segment, for at
// most MaxActive segments.
func (s *Session) storeProvisional(c *caption) {
	if _, tracked := s.active[c.segment]; !tracked && len(s.active) >= s.cfg.MaxActive {
		// Only a misbehaving source has this many unfinished segments. Drop
		// the oldest one's provisional; its final is still welcome later.
		delete(s.active, lowestSegment(s.active))
	}
	s.active[c.segment] = c
}

// lowestSegment returns the smallest key in m, which must not be empty.
// It scans at most MaxActive entries.
func lowestSegment(m map[int64]*caption) int64 {
	first := true
	var lowest int64
	for seg := range m {
		if first || seg < lowest {
			lowest = seg
			first = false
		}
	}
	return lowest
}
