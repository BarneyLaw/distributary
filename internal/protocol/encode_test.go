package protocol

import (
	"encoding/json"
	"math"
	"testing"
)

func TestEncodeMatchesSpec(t *testing.T) {
	caption := CaptionUpdate{
		Session:  "L1",
		Segment:  42,
		Revision: 3,
		SourceTS: 1760000000.123,
		Final:    true,
		Text:     "TCP uses cumulative acknowledgements.",
	}
	if got := string(caption.Encode()); got != specCaption {
		t.Errorf("Encode =\n%s\nwant\n%s", got, specCaption)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	captions := []CaptionUpdate{
		{Session: "L1", Segment: 1, Revision: 1, SourceTS: 1760000000.5, Text: "TCP uses"},
		{Session: "L1", Segment: 1 << 40, Revision: 77, SourceTS: 1e9, Final: true, Text: `quotes " and \ and <html> & 日本`},
		{Segment: 3, Revision: 2, SourceTS: 0.000001, Text: ""},
	}
	for _, caption := range captions {
		raw := caption.Encode()
		msg, err := DecodeSourceMessage(raw)
		if err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		want := caption
		want.Type = TypeCaptionUpdate
		if got := *msg.(*CaptionUpdate); got != want {
			t.Errorf("round trip: got %+v, want %+v", got, want)
		}
	}
}

func TestEncodeAlwaysSetsType(t *testing.T) {
	caption := CaptionUpdate{Type: "WRONG", Segment: 1, Revision: 1, SourceTS: 1, Text: "x"}
	var decoded map[string]any
	if err := json.Unmarshal(caption.Encode(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != TypeCaptionUpdate {
		t.Errorf("type = %v, want %s", decoded["type"], TypeCaptionUpdate)
	}
	if caption.Type != "WRONG" {
		t.Error("Encode modified its receiver")
	}
}

func TestEncodeDoesNotEscapeHTML(t *testing.T) {
	caption := CaptionUpdate{Segment: 1, Revision: 1, SourceTS: 1, Text: "a<b>&c"}
	want := `{"type":"CAPTION_UPDATE","session":"","segment":1,"revision":1,"source_ts":1,"final":false,"text":"a<b>&c"}`
	if got := string(caption.Encode()); got != want {
		t.Errorf("Encode = %s, want %s", got, want)
	}
}

func TestEncodeNonFiniteSourceTS(t *testing.T) {
	for _, sourceTS := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		caption := CaptionUpdate{Segment: 1, Revision: 1, SourceTS: sourceTS, Text: "x"}
		if raw := caption.Encode(); !json.Valid(raw) {
			t.Errorf("SourceTS %v: invalid JSON %q", sourceTS, raw)
		}
	}
}

func TestEncodeHistory(t *testing.T) {
	firstCaption := (&CaptionUpdate{Session: "L1", Segment: 1, Revision: 3, SourceTS: 10, Final: true, Text: "one"}).Encode()
	secondCaption := (&CaptionUpdate{Session: "L1", Segment: 2, Revision: 1, SourceTS: 11, Final: true, Text: "two"}).Encode()

	testCases := []struct {
		name  string
		items [][]byte
		want  string
	}{
		{"nil", nil, `{"type":"HISTORY","items":[]}`},
		{"empty", [][]byte{}, `{"type":"HISTORY","items":[]}`},
		{"one", [][]byte{firstCaption}, `{"type":"HISTORY","items":[` + string(firstCaption) + `]}`},
		{"two", [][]byte{firstCaption, secondCaption}, `{"type":"HISTORY","items":[` + string(firstCaption) + `,` + string(secondCaption) + `]}`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := EncodeHistory(testCase.items)
			if string(raw) != testCase.want {
				t.Errorf("EncodeHistory = %s, want %s", raw, testCase.want)
			}
			if len(raw) != cap(raw) {
				t.Errorf("EncodeHistory allocated %d bytes for %d bytes of output", cap(raw), len(raw))
			}
			var history History
			if err := json.Unmarshal(raw, &history); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}
			if history.Type != TypeHistory || history.Items == nil || len(history.Items) != len(testCase.items) {
				t.Errorf("parsed back as %+v", history)
			}
		})
	}
}

func TestEncodeHistoryOrderPreserved(t *testing.T) {
	var items [][]byte
	for segment := int64(1); segment <= 5; segment++ {
		items = append(items, (&CaptionUpdate{Segment: segment, Revision: 1, SourceTS: float64(segment), Final: true, Text: "x"}).Encode())
	}
	var history History
	if err := json.Unmarshal(EncodeHistory(items), &history); err != nil {
		t.Fatal(err)
	}
	for index, item := range history.Items {
		if item.Segment != int64(index+1) {
			t.Errorf("item %d has segment %d, want %d", index, item.Segment, index+1)
		}
	}
}
