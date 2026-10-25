package protocol

import (
	"bytes"
	"encoding/json"
	"math"
)

// Encode returns the compact JSON for c, with "type" always CAPTION_UPDATE.
// Session encodes each caption once and every client reuses the bytes.
//
// HTML characters are not escaped, so text is sent as-is. A non-finite
// SourceTS, which decoding never produces, is written as 0 so the output is
// always valid JSON.
func (c *CaptionUpdate) Encode() []byte {
	out := *c
	out.Type = TypeCaptionUpdate
	if math.IsInf(out.SourceTS, 0) || math.IsNaN(out.SourceTS) {
		out.SourceTS = 0
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		// Unreachable: every field is a string, bool, integer or finite float.
		return nil
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

var (
	historyPrefix = []byte(`{"type":"HISTORY","items":[`)
	historySuffix = []byte(`]}`)
)

// EncodeHistory builds a HISTORY message from already-encoded CaptionUpdate
// bytes (from Encode), oldest first, without re-marshalling anything. Each
// item must be a valid JSON object. No items gives "items":[].
func EncodeHistory(items [][]byte) []byte {
	size := len(historyPrefix) + len(historySuffix) + len(items)
	for _, item := range items {
		size += len(item)
	}
	out := make([]byte, 0, size)
	out = append(out, historyPrefix...)
	for i, item := range items {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, item...)
	}
	return append(out, historySuffix...)
}
