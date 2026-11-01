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
func DecodeHello(rawMessage []byte) (any, error) {
	message, parseErr := parseObject(rawMessage)
	if parseErr != nil {
		return nil, parseErr
	}
	messageType, typeErr := message.messageType()
	if typeErr != nil {
		return nil, typeErr
	}
	switch messageType {
	case TypeSourceHello:
		return &SourceHello{
			Type:    TypeSourceHello,
			Session: message.informationalString("session"),
		}, nil
	case TypeClientHello:
		return orNil(decodeClientHello(message))
	}
	return nil, fmt.Errorf("%w: %q is not a hello", ErrUnknownType, messageType)
}

// DecodeSourceMessage decodes a message received on the source connection
// after SOURCE_HELLO. It returns *CaptionUpdate or *SourceDone. Any other
// type returns ErrUnknownType, which the caller should count and ignore.
// A returned *CaptionUpdate has every required field present,
// Segment >= 1, Revision >= 1 and a finite SourceTS > 0. Unknown fields are
// dropped.
func DecodeSourceMessage(rawMessage []byte) (any, error) {
	message, parseErr := parseObject(rawMessage)
	if parseErr != nil {
		return nil, parseErr
	}
	messageType, typeErr := message.messageType()
	if typeErr != nil {
		return nil, typeErr
	}
	switch messageType {
	case TypeCaptionUpdate:
		return orNil(decodeCaptionUpdate(message))
	case TypeSourceDone:
		return &SourceDone{
			Type:     TypeSourceDone,
			Session:  message.informationalString("session"),
			SourceTS: message.informationalFloat("source_ts"),
			Segments: message.informationalInt("segments"),
		}, nil
	}
	return nil, fmt.Errorf("%w: %q on source connection", ErrUnknownType, messageType)
}

// orNil converts a typed result to any, returning an untyped nil on error.
// Returning a nil *T inside an `any` would make `msg != nil` true for callers.
func orNil[T any](decoded *T, decodeErr error) (any, error) {
	if decodeErr != nil {
		return nil, decodeErr
	}
	return decoded, nil
}

// decodeCaptionUpdate reads every CAPTION_UPDATE field strictly, then applies
// the range rules in validate.go.
func decodeCaptionUpdate(message jsonObject) (*CaptionUpdate, error) {
	segment, segmentErr := message.requiredInt("segment")
	if segmentErr != nil {
		return nil, segmentErr
	}
	revision, revisionErr := message.requiredInt("revision")
	if revisionErr != nil {
		return nil, revisionErr
	}
	sourceTS, sourceTSErr := message.requiredFloat("source_ts")
	if sourceTSErr != nil {
		return nil, sourceTSErr
	}
	final, finalErr := message.requiredBool("final")
	if finalErr != nil {
		return nil, finalErr
	}
	text, textErr := message.requiredString("text")
	if textErr != nil {
		return nil, textErr
	}
	caption := &CaptionUpdate{
		Type:     TypeCaptionUpdate,
		Session:  message.informationalString("session"),
		Segment:  segment,
		Revision: revision,
		SourceTS: sourceTS,
		Final:    final,
		Text:     text,
	}
	if rangeErr := validateCaptionUpdate(caption); rangeErr != nil {
		return nil, rangeErr
	}
	return caption, nil
}

// decodeClientHello reads the resume fields, defaulting absent ones to 0, then
// validates and clamps them.
func decodeClientHello(message jsonObject) (*ClientHello, error) {
	lastSegment, lastSegmentErr := message.optionalInt("last_segment", 0)
	if lastSegmentErr != nil {
		return nil, lastSegmentErr
	}
	lastRevision, lastRevisionErr := message.optionalInt("last_revision", 0)
	if lastRevisionErr != nil {
		return nil, lastRevisionErr
	}
	historySeconds, historySecondsErr := message.optionalFloat("history_seconds", 0)
	if historySecondsErr != nil {
		return nil, historySecondsErr
	}
	hello := &ClientHello{
		Type:           TypeClientHello,
		ClientID:       message.informationalInt("client_id"),
		Session:        message.informationalString("session"),
		LastSegment:    lastSegment,
		LastRevision:   lastRevision,
		HistorySeconds: historySeconds,
	}
	if rangeErr := validateClientHello(hello); rangeErr != nil {
		return nil, rangeErr
	}
	return hello, nil
}
