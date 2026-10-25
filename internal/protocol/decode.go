package protocol

import "fmt"

// Decoding rules:
//
//   - Fields the server acts on are strict: a missing required field or a
//     wrong JSON type rejects the message.
//   - Fields the server never acts on (session, client_id, all of
//     SOURCE_DONE) are informational: a bad value becomes the zero value and
//     never rejects the message.
//   - Unknown fields are ignored (spec 4.1). A JSON null counts as absent.

// DecodeHello decodes the first message on a connection.
// It returns *SourceHello or *ClientHello. The caller closes the connection
// with code 1008 on any error. A returned *ClientHello is normalised: missing
// resume fields are 0 and HistorySeconds is clamped to [0, MaxHistorySeconds].
func DecodeHello(raw []byte) (any, error) {
	obj, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	t, err := obj.messageType()
	if err != nil {
		return nil, err
	}
	switch t {
	case TypeSourceHello:
		return &SourceHello{
			Type:    TypeSourceHello,
			Session: obj.informationalString("session"),
		}, nil
	case TypeClientHello:
		return orNil(decodeClientHello(obj))
	}
	return nil, fmt.Errorf("%w: %q is not a hello", ErrUnknownType, t)
}

// DecodeSourceMessage decodes a message received on the source connection
// after SOURCE_HELLO. It returns *CaptionUpdate or *SourceDone. Any other
// type returns ErrUnknownType, which the caller should count and ignore.
// A returned *CaptionUpdate has every required field present,
// Segment >= 1, Revision >= 1 and a finite SourceTS > 0. Unknown fields are
// dropped.
func DecodeSourceMessage(raw []byte) (any, error) {
	obj, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	t, err := obj.messageType()
	if err != nil {
		return nil, err
	}
	switch t {
	case TypeCaptionUpdate:
		return orNil(decodeCaptionUpdate(obj))
	case TypeSourceDone:
		return &SourceDone{
			Type:     TypeSourceDone,
			Session:  obj.informationalString("session"),
			SourceTS: obj.informationalFloat("source_ts"),
			Segments: obj.informationalInt("segments"),
		}, nil
	}
	return nil, fmt.Errorf("%w: %q on source connection", ErrUnknownType, t)
}

// orNil converts a typed result to any, returning an untyped nil on error.
// Returning a nil *T inside an `any` would make `msg != nil` true for callers.
func orNil[T any](msg *T, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func decodeCaptionUpdate(obj object) (*CaptionUpdate, error) {
	segment, err := obj.requiredInt("segment")
	if err != nil {
		return nil, err
	}
	revision, err := obj.requiredInt("revision")
	if err != nil {
		return nil, err
	}
	sourceTS, err := obj.requiredFloat("source_ts")
	if err != nil {
		return nil, err
	}
	final, err := obj.requiredBool("final")
	if err != nil {
		return nil, err
	}
	text, err := obj.requiredString("text")
	if err != nil {
		return nil, err
	}
	c := &CaptionUpdate{
		Type:     TypeCaptionUpdate,
		Session:  obj.informationalString("session"),
		Segment:  segment,
		Revision: revision,
		SourceTS: sourceTS,
		Final:    final,
		Text:     text,
	}
	if err := validateCaptionUpdate(c); err != nil {
		return nil, err
	}
	return c, nil
}

func decodeClientHello(obj object) (*ClientHello, error) {
	lastSegment, err := obj.optionalInt("last_segment", 0)
	if err != nil {
		return nil, err
	}
	lastRevision, err := obj.optionalInt("last_revision", 0)
	if err != nil {
		return nil, err
	}
	historySeconds, err := obj.optionalFloat("history_seconds", 0)
	if err != nil {
		return nil, err
	}
	h := &ClientHello{
		Type:           TypeClientHello,
		ClientID:       obj.informationalInt("client_id"),
		Session:        obj.informationalString("session"),
		LastSegment:    lastSegment,
		LastRevision:   lastRevision,
		HistorySeconds: historySeconds,
	}
	if err := validateClientHello(h); err != nil {
		return nil, err
	}
	return h, nil
}
