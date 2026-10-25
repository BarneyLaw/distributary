package protocol

import "fmt"

// MaxHistorySeconds is the largest recovery window a client may request
// (spec section 4.2).
const MaxHistorySeconds = 60

// validateCaptionUpdate checks the value ranges of a decoded caption.
// Semantic checks (already final, stale revision, too old) belong to session.
func validateCaptionUpdate(c *CaptionUpdate) error {
	if c.Segment < 1 {
		return fmt.Errorf("%w: segment must be >= 1, got %d", ErrOutOfRange, c.Segment)
	}
	if c.Revision < 1 {
		return fmt.Errorf("%w: revision must be >= 1, got %d", ErrOutOfRange, c.Revision)
	}
	if c.SourceTS <= 0 {
		return fmt.Errorf("%w: source_ts must be > 0, got %v", ErrOutOfRange, c.SourceTS)
	}
	return nil
}

// validateClientHello rejects negative resume fields and clamps
// HistorySeconds to [0, MaxHistorySeconds] in place.
func validateClientHello(h *ClientHello) error {
	if h.LastSegment < 0 {
		return fmt.Errorf("%w: last_segment must be >= 0, got %d", ErrOutOfRange, h.LastSegment)
	}
	if h.LastRevision < 0 {
		return fmt.Errorf("%w: last_revision must be >= 0, got %d", ErrOutOfRange, h.LastRevision)
	}
	h.HistorySeconds = min(max(h.HistorySeconds, 0), MaxHistorySeconds)
	return nil
}
