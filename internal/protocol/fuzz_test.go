package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

// Run longer locally with: go test ./internal/protocol -fuzz=FuzzDecode -fuzztime=30s
// Plain `go test` runs only the seed corpus below.

var fuzzSeeds = []string{
	specCaption,
	`{"type":"SOURCE_HELLO","session":"L1"}`,
	`{"type":"SOURCE_DONE","session":"L1","source_ts":1,"segments":3}`,
	`{"type":"CLIENT_HELLO","client_id":17,"session":"L1","last_segment":38,"last_revision":3,"history_seconds":30}`,
	`{"type":"CAPTION_UPDATE","segment":1e400,"revision":-0,"source_ts":-1e-400,"final":null,"text":"\ud800"}`,
	`{"type":"CLIENT_HELLO","history_seconds":-1e308,"last_segment":9223372036854775808}`,
	`{"type":"CAPTION_UPDATE","type":"SOURCE_HELLO"}`,
	`null`, `[]`, `""`, `{}`, ``, "\xff",
}

var sentinelErrors = []error{ErrMalformed, ErrUnknownType, ErrMissingField, ErrBadType, ErrOutOfRange}

// FuzzDecode checks that decoding never panics, every error is one of the
// typed errors, and every accepted message satisfies its documented
// guarantees.
func FuzzDecode(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		msg, err := DecodeSourceMessage(raw)
		checkResult(t, msg, err)
		if c, ok := msg.(*CaptionUpdate); ok {
			checkCaption(t, c)
		}

		msg, err = DecodeHello(raw)
		checkResult(t, msg, err)
		if h, ok := msg.(*ClientHello); ok {
			if h.LastSegment < 0 || h.LastRevision < 0 || h.HistorySeconds < 0 || h.HistorySeconds > MaxHistorySeconds {
				t.Errorf("ClientHello guarantees broken: %+v", *h)
			}
		}
	})
}

func checkResult(t *testing.T, msg any, err error) {
	t.Helper()
	if (msg == nil) == (err == nil) {
		t.Fatalf("exactly one of msg and err must be set: msg=%v err=%v", msg, err)
	}
	if err == nil {
		return
	}
	for _, s := range sentinelErrors {
		if errors.Is(err, s) {
			return
		}
	}
	t.Errorf("untyped error: %v", err)
}

// checkCaption verifies the decode guarantees and that the caption survives
// an encode/decode round trip unchanged.
func checkCaption(t *testing.T, c *CaptionUpdate) {
	t.Helper()
	if c.Segment < 1 || c.Revision < 1 || !(c.SourceTS > 0) {
		t.Fatalf("CaptionUpdate guarantees broken: %+v", *c)
	}
	raw := c.Encode()
	if !json.Valid(raw) {
		t.Fatalf("Encode produced invalid JSON: %q", raw)
	}
	again, err := DecodeSourceMessage(raw)
	if err != nil {
		t.Fatalf("re-decode of %q: %v", raw, err)
	}
	if *again.(*CaptionUpdate) != *c {
		t.Errorf("round trip changed caption: %+v -> %+v", *c, *again.(*CaptionUpdate))
	}
	if !json.Valid(EncodeHistory([][]byte{raw, raw})) {
		t.Error("EncodeHistory produced invalid JSON")
	}
}
