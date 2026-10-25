package protocol

import "errors"

// Decode errors. Returned errors wrap one of these with the offending field
// name, so callers compare with errors.Is and stats can count drops by reason.
var (
	ErrMalformed    = errors.New("protocol: malformed message")
	ErrUnknownType  = errors.New("protocol: unknown or unexpected message type")
	ErrMissingField = errors.New("protocol: required field missing")
	ErrBadType      = errors.New("protocol: field has wrong JSON type")
	ErrOutOfRange   = errors.New("protocol: field value out of range")
)
