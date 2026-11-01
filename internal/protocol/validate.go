package protocol

import "fmt"

// MaxHistorySeconds is the largest recovery window a client may request
// (spec section 4.2).
const MaxHistorySeconds = 60

// validateCaptionUpdate checks the value ranges of a decoded caption.
// Semantic checks (already final, stale revision, too old) belong to session.
func validateCaptionUpdate(caption *CaptionUpdate) error {
	if caption.Segment < 1 {
		return fmt.Errorf("%w: segment must be >= 1, got %d", ErrOutOfRange, caption.Segment)
	}
	if caption.Revision < 1 {
		return fmt.Errorf("%w: revision must be >= 1, got %d", ErrOutOfRange, caption.Revision)
	}
	if caption.SourceTS <= 0 {
		return fmt.Errorf("%w: source_ts must be > 0, got %v", ErrOutOfRange, caption.SourceTS)
	}
	return nil
}

// validateClientHello rejects negative resume fields and clamps
// HistorySeconds to [0, MaxHistorySeconds] in place.
func validateClientHello(hello *ClientHello) error {
	if hello.LastSegment < 0 {
		return fmt.Errorf("%w: last_segment must be >= 0, got %d", ErrOutOfRange, hello.LastSegment)
	}
	if hello.LastRevision < 0 {
		return fmt.Errorf("%w: last_revision must be >= 0, got %d", ErrOutOfRange, hello.LastRevision)
	}
	hello.HistorySeconds = min(max(hello.HistorySeconds, 0), MaxHistorySeconds)
	return nil
}
