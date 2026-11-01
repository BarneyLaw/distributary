package protocol

import (
	"bytes"
	"encoding/json"
	"math"
)

// Encode returns the compact JSON for caption, with "type" always
// CAPTION_UPDATE. Session encodes each caption once and every client reuses
// the bytes.
//
// HTML characters are not escaped, so text is sent as-is. A non-finite
// SourceTS, which decoding never produces, is written as 0 so the output is
// always valid JSON.
func (caption *CaptionUpdate) Encode() []byte {
	wireCaption := *caption
	wireCaption.Type = TypeCaptionUpdate
	if math.IsInf(wireCaption.SourceTS, 0) || math.IsNaN(wireCaption.SourceTS) {
		wireCaption.SourceTS = 0
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	// The error is ignored because it cannot happen: every field is a string,
	// bool, integer or (after the check above) finite float, and writing to a
	// bytes.Buffer never fails. FuzzDecode checks the output is valid JSON.
	_ = encoder.Encode(wireCaption)
	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
}

var (
	historyPrefix = []byte(`{"type":"HISTORY","items":[`)
	historySuffix = []byte(`]}`)
)

// EncodeHistory builds a HISTORY message from already-encoded CaptionUpdate
// bytes (from Encode), oldest first, without re-marshalling anything. Each
// item must be a valid JSON object. No items gives "items":[].
func EncodeHistory(encodedCaptions [][]byte) []byte {
	commaCount := max(len(encodedCaptions)-1, 0)
	totalSize := len(historyPrefix) + len(historySuffix) + commaCount
	for _, encodedCaption := range encodedCaptions {
		totalSize += len(encodedCaption)
	}
	history := make([]byte, 0, totalSize)
	history = append(history, historyPrefix...)
	for index, encodedCaption := range encodedCaptions {
		if index > 0 {
			history = append(history, ',')
		}
		history = append(history, encodedCaption...)
	}
	return append(history, historySuffix...)
}
