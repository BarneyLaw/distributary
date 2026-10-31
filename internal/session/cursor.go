package session

import "slices"

// Cursor is one client's read position. Get it from Join and pass the same
// value to every Pick for that client. A zero Cursor is not valid.
type Cursor struct {
	finalSeq   uint64          // fseq of the last final handed out (0 = none)
	minSegment int64           // CLIENT_HELLO's last_segment: never send finals at or below it
	sentRev    map[int64]int64 // segment -> last provisional revision handed out
	// (seeded by Join with the current revisions)
}

// Pick returns up to max frames for one client and advances its cursor:
//
//  1. pending finals, oldest first, skipping segments <= the hello's
//     last_segment;
//  2. then, only if provisionals is true and room is left, the newest
//     provisional of each active segment (lowest segment first) whose
//     revision is higher than the last one handed to this client.
//
// Pick never returns more than max frames (max <= 0 returns none) and never
// returns a frame it has already returned for this cursor. fanout passes its
// remaining send credit as max, and provisionals=false in degraded mode.
//
// wake is closed by the next Accepted Apply. It is read under the same lock
// as the frames, so a caller that sends the frames and then waits on wake
// cannot miss an update.
//
// behind is true when the next final this cursor needs has already been
// evicted from the ring; frames is then empty. The caller should close the
// connection (code 1013); the client reconnects and recovers through HISTORY.
func (s *Session) Pick(cur *Cursor, max int, provisionals bool) (frames [][]byte, wake <-chan struct{}, behind bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	wake = s.changed
	if max <= 0 {
		return nil, wake, false
	}

	if cur.finalSeq+1 < s.finals.oldestSeq() && s.floor > cur.minSegment {
		return nil, wake, true
	}

	for cur.finalSeq < s.finals.newestSeq() && len(frames) < max {
		cur.finalSeq++
		c := s.finals.get(cur.finalSeq)
		delete(cur.sentRev, c.segment) // segment is terminal, keeps sentRev bounded
		if c.segment <= cur.minSegment {
			continue // client already has it
		}
		frames = append(frames, c.frame)

	}

	if provisionals && len(frames) < max {
		segments := make([]int64, 0, len(s.active))
		for seg := range s.active {
			segments = append(segments, seg)
		}
		slices.Sort(segments) // lowest segment first

		for _, seg := range segments {
			if len(frames) >= max {
				break
			}
			c := s.active[seg]
			if c.revision > cur.sentRev[seg] {
				frames = append(frames, c.frame)
				cur.sentRev[seg] = c.revision
			}
		}
	}

	return frames, wake, false
}
