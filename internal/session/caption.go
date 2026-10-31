package session

import (
	"github.com/BarneyLaw/distributary/internal/protocol"
)

// caption is one stored revision of one segment.
//
// It is never modified after newCaption returns. That is what lets any
// number of client goroutines read it, and send its frame, without a lock.
type caption struct {
	segment int64
	revision int64
	sourceTS float64
	final bool

	// frame is the exact CAPTION_UPDATE JSON every client is sent. It is
	// encoded once here and the same slice is handed to every client, so it
	// must be treated as read-only forever.
	frame []byte
}

// newCaption copies the fields session needs out of a decoded update and
// encodes the frame once. It keeps no reference to u, so nothing outside the
// package can change a stored caption. Text is not stored separately: it is
// inside frame, and session's logic never looks at it.
//
// Encode also sets "type" and drops fields the source added that the
// protocol does not define (spec 4.1 says to ignore them).
func newCaption(u *protocol.CaptionUpdate) *caption {
	return &caption{
		segment:  u.Segment,
		revision: u.Revision,
		sourceTS: u.SourceTS,
		final:    u.Final,
		frame:    u.Encode(),
	}
}
