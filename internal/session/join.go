package session

import "github.com/BarneyLaw/distributary/internal/protocol"

// Join is called once per connection, after CLIENT_HELLO. It returns the
// complete HISTORY frame to send first, and the cursor to use from then on.
//
// HISTORY holds the ring's finals, oldest first, with
//
//	segment > h.LastSegment  and  source_ts >= newest source_ts - h.HistorySeconds
//
// and is empty when h.HistorySeconds is 0. The cursor starts after the
// newest final in the ring, NOT after the last HISTORY item: older finals the
// client asked not to receive must not leak in later through Pick. Both are
// taken under one lock, so no final can fall between HISTORY and the live
// stream, and none can appear in both.
//
// The cursor also records the current provisional of every active segment as
// already sent. CLIENT_HELLO only reports finals, so a reconnecting client may
// already hold that provisional, and sending it again would be a duplicate.
// The client picks the segment up from its next revision or its final.
func (s *Session) Join(h *protocol.ClientHello) (history []byte, cur Cursor) {
	// TODO(session): step 2. Under s.mu.RLock:
	//   - walk fseq from s.finals.oldestSeq() to s.finals.newestSeq(),
	//     collecting c.frame for finals that pass both filters above
	//   - cursor: finalSeq = s.finals.newestSeq(), minSegment = h.LastSegment,
	//     sentRev = the current revision of every segment in s.active
	//   - return protocol.EncodeHistory(frames), cursor
	s.mu.RLock()
	defer s.mu.RUnlock()

	var frames [][]byte
	if h.HistorySeconds > 0 {
		cutoff := s.newestTS - h.HistorySeconds
		for fseq := s.finals.oldestSeq(); fseq <= s.finals.newestSeq(); fseq++ {
			c := s.finals.get(fseq)
			if c.segment > h.LastSegment && c.sourceTS >= cutoff {
				frames = append(frames, c.frame)
			}
		}
	}
	sentRev := make(map[int64]int64, len(s.active))
	for seg, c := range s.active {
		sentRev[seg] = c.revision // may already be held by a reconnecting client
	}

	return protocol.EncodeHistory(frames), Cursor{
		finalSeq:   s.finals.newestSeq(),
		minSegment: h.LastSegment,
		sentRev:    sentRev,
	}
}
