package protocol

import (
	"encoding/json"
	"testing"
)

// Byte-for-byte output of Python's json.dumps as used by caption_source.py and
// load_clients.py: spaces after ':' and ',', non-ASCII as \u escapes, and
// time.time() timestamps with microsecond digits.
const (
	pySourceHello = `{"type": "SOURCE_HELLO", "session": "L1"}`
	pyCaption     = `{"type": "CAPTION_UPDATE", "session": "L1", "segment": 1, "revision": 1, "source_ts": 1792900517.786676, "final": false, "text": "TCP uses"}`
	pyCaptionUni  = `{"type": "CAPTION_UPDATE", "session": "L1", "segment": 2, "revision": 3, "source_ts": 1792900518.5, "final": true, "text": "café — “quotes”"}`
	pySourceDone  = `{"type": "SOURCE_DONE", "session": "L1", "source_ts": 1792900517.7866938, "segments": 10}`
	pyClientHello = `{"type": "CLIENT_HELLO", "client_id": 3, "session": "L1", "last_segment": 0, "last_revision": 0, "history_seconds": 30}`
)

func TestHarnessSourceMessages(t *testing.T) {
	msg, err := DecodeHello([]byte(pySourceHello))
	if err != nil {
		t.Fatalf("SOURCE_HELLO: %v", err)
	}
	if got := *msg.(*SourceHello); got != (SourceHello{Type: TypeSourceHello, Session: "L1"}) {
		t.Errorf("SOURCE_HELLO = %+v", got)
	}

	msg, err = DecodeSourceMessage([]byte(pyCaption))
	if err != nil {
		t.Fatalf("CAPTION_UPDATE: %v", err)
	}
	want := CaptionUpdate{Type: TypeCaptionUpdate, Session: "L1", Segment: 1, Revision: 1, SourceTS: 1792900517.786676, Text: "TCP uses"}
	if got := *msg.(*CaptionUpdate); got != want {
		t.Errorf("CAPTION_UPDATE = %+v, want %+v", got, want)
	}

	msg, err = DecodeSourceMessage([]byte(pyCaptionUni))
	if err != nil {
		t.Fatalf("unicode CAPTION_UPDATE: %v", err)
	}
	if got := msg.(*CaptionUpdate).Text; got != "café — “quotes”" {
		t.Errorf("text = %q, want unescaped unicode", got)
	}

	msg, err = DecodeSourceMessage([]byte(pySourceDone))
	if err != nil {
		t.Fatalf("SOURCE_DONE: %v", err)
	}
	if got := msg.(*SourceDone).Segments; got != 10 {
		t.Errorf("segments = %d, want 10", got)
	}
}

func TestHarnessClientHello(t *testing.T) {
	msg, err := DecodeHello([]byte(pyClientHello))
	if err != nil {
		t.Fatalf("CLIENT_HELLO: %v", err)
	}
	want := ClientHello{Type: TypeClientHello, ClientID: 3, Session: "L1", HistorySeconds: 30}
	if got := *msg.(*ClientHello); got != want {
		t.Errorf("CLIENT_HELLO = %+v, want %+v", got, want)
	}
}

// TestHarnessReadsEncodedCaption parses Encode output the way load_clients.py
// does (generic JSON, then int/float/bool of each field) and checks the
// timestamp survives to the microsecond, since Caption Age is computed from it.
func TestHarnessReadsEncodedCaption(t *testing.T) {
	msg, err := DecodeSourceMessage([]byte(pyCaptionUni))
	if err != nil {
		t.Fatal(err)
	}
	in := msg.(*CaptionUpdate)

	var out map[string]any
	if err := json.Unmarshal(in.Encode(), &out); err != nil {
		t.Fatalf("Encode output is not JSON: %v", err)
	}
	if out["type"] != TypeCaptionUpdate || out["segment"] != 2.0 || out["revision"] != 3.0 ||
		out["final"] != true || out["text"] != in.Text || out["source_ts"] != in.SourceTS {
		t.Errorf("harness would read %v, want fields of %+v", out, *in)
	}

	var h struct{ Items []map[string]any }
	if err := json.Unmarshal(EncodeHistory([][]byte{in.Encode()}), &h); err != nil || len(h.Items) != 1 || h.Items[0]["segment"] != 2.0 {
		t.Errorf("harness would read HISTORY items %v (err %v)", h.Items, err)
	}
}

// TestDuplicateKeysLastWins records encoding/json's behaviour for repeated
// keys: the last value is used. Python's json.loads does the same, so the
// server and the harness agree on what such a message means.
func TestDuplicateKeysLastWins(t *testing.T) {
	msg, err := DecodeSourceMessage([]byte(`{"type":"CAPTION_UPDATE","type":"SOURCE_DONE","segments":4}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := msg.(*SourceDone); !ok {
		t.Errorf("got %T, want *SourceDone (last type wins)", msg)
	}

	msg, err = DecodeSourceMessage([]byte(`{"type":"CAPTION_UPDATE","segment":1,"segment":7,"revision":1,"source_ts":1,"final":true,"text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.(*CaptionUpdate).Segment; got != 7 {
		t.Errorf("segment = %d, want 7 (last value wins)", got)
	}
}

// TestKeysAreCaseSensitive checks that "SEGMENT" is not mistaken for
// "segment": unlike encoding/json struct decoding, map lookup is exact.
func TestKeysAreCaseSensitive(t *testing.T) {
	_, err := DecodeSourceMessage([]byte(`{"type":"CAPTION_UPDATE","SEGMENT":1,"revision":1,"source_ts":1,"final":true,"text":"x"}`))
	if err == nil {
		t.Error("SEGMENT accepted as segment")
	}
}
