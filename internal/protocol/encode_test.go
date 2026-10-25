package protocol

import (
	"encoding/json"
	"math"
	"testing"
)

func TestEncodeMatchesSpec(t *testing.T) {
	c := CaptionUpdate{
		Session:  "L1",
		Segment:  42,
		Revision: 3,
		SourceTS: 1760000000.123,
		Final:    true,
		Text:     "TCP uses cumulative acknowledgements.",
	}
	if got := string(c.Encode()); got != specCaption {
		t.Errorf("Encode =\n%s\nwant\n%s", got, specCaption)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	tests := []CaptionUpdate{
		{Session: "L1", Segment: 1, Revision: 1, SourceTS: 1760000000.5, Text: "TCP uses"},
		{Session: "L1", Segment: 1 << 40, Revision: 77, SourceTS: 1e9, Final: true, Text: `quotes " and \ and <html> & 日本`},
		{Segment: 3, Revision: 2, SourceTS: 0.000001, Text: ""},
	}
	for _, c := range tests {
		raw := c.Encode()
		msg, err := DecodeSourceMessage(raw)
		if err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		want := c
		want.Type = TypeCaptionUpdate
		if got := *msg.(*CaptionUpdate); got != want {
			t.Errorf("round trip: got %+v, want %+v", got, want)
		}
	}
}

func TestEncodeAlwaysSetsType(t *testing.T) {
	c := CaptionUpdate{Type: "WRONG", Segment: 1, Revision: 1, SourceTS: 1, Text: "x"}
	var out map[string]any
	if err := json.Unmarshal(c.Encode(), &out); err != nil {
		t.Fatal(err)
	}
	if out["type"] != TypeCaptionUpdate {
		t.Errorf("type = %v, want %s", out["type"], TypeCaptionUpdate)
	}
	if c.Type != "WRONG" {
		t.Error("Encode modified its receiver")
	}
}

func TestEncodeDoesNotEscapeHTML(t *testing.T) {
	c := CaptionUpdate{Segment: 1, Revision: 1, SourceTS: 1, Text: "a<b>&c"}
	want := `{"type":"CAPTION_UPDATE","session":"","segment":1,"revision":1,"source_ts":1,"final":false,"text":"a<b>&c"}`
	if got := string(c.Encode()); got != want {
		t.Errorf("Encode = %s, want %s", got, want)
	}
}

func TestEncodeNonFiniteSourceTS(t *testing.T) {
	for _, ts := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		c := CaptionUpdate{Segment: 1, Revision: 1, SourceTS: ts, Text: "x"}
		if raw := c.Encode(); !json.Valid(raw) {
			t.Errorf("SourceTS %v: invalid JSON %q", ts, raw)
		}
	}
}

func TestEncodeHistory(t *testing.T) {
	a := (&CaptionUpdate{Session: "L1", Segment: 1, Revision: 3, SourceTS: 10, Final: true, Text: "one"}).Encode()
	b := (&CaptionUpdate{Session: "L1", Segment: 2, Revision: 1, SourceTS: 11, Final: true, Text: "two"}).Encode()

	tests := []struct {
		name  string
		items [][]byte
		want  string
	}{
		{"nil", nil, `{"type":"HISTORY","items":[]}`},
		{"empty", [][]byte{}, `{"type":"HISTORY","items":[]}`},
		{"one", [][]byte{a}, `{"type":"HISTORY","items":[` + string(a) + `]}`},
		{"two", [][]byte{a, b}, `{"type":"HISTORY","items":[` + string(a) + `,` + string(b) + `]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := EncodeHistory(tt.items)
			if string(raw) != tt.want {
				t.Errorf("EncodeHistory = %s, want %s", raw, tt.want)
			}
			var h History
			if err := json.Unmarshal(raw, &h); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}
			if h.Type != TypeHistory || h.Items == nil || len(h.Items) != len(tt.items) {
				t.Errorf("parsed back as %+v", h)
			}
		})
	}
}

func TestEncodeHistoryOrderPreserved(t *testing.T) {
	var items [][]byte
	for seg := int64(1); seg <= 5; seg++ {
		items = append(items, (&CaptionUpdate{Segment: seg, Revision: 1, SourceTS: float64(seg), Final: true, Text: "x"}).Encode())
	}
	var h History
	if err := json.Unmarshal(EncodeHistory(items), &h); err != nil {
		t.Fatal(err)
	}
	for i, item := range h.Items {
		if item.Segment != int64(i+1) {
			t.Errorf("item %d has segment %d, want %d", i, item.Segment, i+1)
		}
	}
}
