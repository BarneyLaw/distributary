package protocol

import (
	"errors"
	"testing"
)

// specCaption is the CAPTION_UPDATE example from spec section 4.1.
const specCaption = `{"type":"CAPTION_UPDATE","session":"L1","segment":42,"revision":3,"source_ts":1760000000.123,"final":true,"text":"TCP uses cumulative acknowledgements."}`

func TestDecodeSourceMessageCaption(t *testing.T) {
	msg, err := DecodeSourceMessage([]byte(specCaption))
	if err != nil {
		t.Fatalf("DecodeSourceMessage: %v", err)
	}
	got, ok := msg.(*CaptionUpdate)
	if !ok {
		t.Fatalf("got %T, want *CaptionUpdate", msg)
	}
	want := CaptionUpdate{
		Type:     TypeCaptionUpdate,
		Session:  "L1",
		Segment:  42,
		Revision: 3,
		SourceTS: 1760000000.123,
		Final:    true,
		Text:     "TCP uses cumulative acknowledgements.",
	}
	if *got != want {
		t.Errorf("got %+v, want %+v", *got, want)
	}
}

func TestDecodeSourceMessageCaptionAccepts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want CaptionUpdate
	}{
		{
			name: "unknown fields ignored",
			raw:  `{"type":"CAPTION_UPDATE","phase":"burst","extra":{"a":[1,2]},"segment":1,"revision":1,"source_ts":1.5,"final":false,"text":"hi"}`,
			want: CaptionUpdate{Type: TypeCaptionUpdate, Segment: 1, Revision: 1, SourceTS: 1.5, Text: "hi"},
		},
		{
			name: "session optional",
			raw:  `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true,"text":"x"}`,
			want: CaptionUpdate{Type: TypeCaptionUpdate, Segment: 1, Revision: 1, SourceTS: 1, Final: true, Text: "x"},
		},
		{
			name: "non-string session ignored",
			raw:  `{"type":"CAPTION_UPDATE","session":7,"segment":1,"revision":1,"source_ts":1,"final":true,"text":"x"}`,
			want: CaptionUpdate{Type: TypeCaptionUpdate, Segment: 1, Revision: 1, SourceTS: 1, Final: true, Text: "x"},
		},
		{
			name: "whole-number floats accepted as integers",
			raw:  `{"type":"CAPTION_UPDATE","segment":2.0,"revision":1e1,"source_ts":1,"final":false,"text":"x"}`,
			want: CaptionUpdate{Type: TypeCaptionUpdate, Segment: 2, Revision: 10, SourceTS: 1, Text: "x"},
		},
		{
			name: "empty text allowed",
			raw:  `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":false,"text":""}`,
			want: CaptionUpdate{Type: TypeCaptionUpdate, Segment: 1, Revision: 1, SourceTS: 1},
		},
		{
			name: "unicode and escapes in text",
			raw:  `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":false,"text":"café \"<b>\" 日本"}`,
			want: CaptionUpdate{Type: TypeCaptionUpdate, Segment: 1, Revision: 1, SourceTS: 1, Text: `café "<b>" 日本`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := DecodeSourceMessage([]byte(tt.raw))
			if err != nil {
				t.Fatalf("DecodeSourceMessage: %v", err)
			}
			if got := *msg.(*CaptionUpdate); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDecodeSourceMessageCaptionRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"not json", `{"type":`, ErrMalformed},
		{"empty", ``, ErrMalformed},
		{"array", `[1,2]`, ErrMalformed},
		{"null", `null`, ErrMalformed},
		{"string", `"CAPTION_UPDATE"`, ErrMalformed},
		{"no type", `{"segment":1}`, ErrUnknownType},
		{"type not string", `{"type":5}`, ErrUnknownType},
		{"unknown type", `{"type":"PING"}`, ErrUnknownType},
		{"hello on source", `{"type":"CLIENT_HELLO"}`, ErrUnknownType},

		{"missing segment", `{"type":"CAPTION_UPDATE","revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrMissingField},
		{"missing revision", `{"type":"CAPTION_UPDATE","segment":1,"source_ts":1,"final":true,"text":"x"}`, ErrMissingField},
		{"missing source_ts", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"final":true,"text":"x"}`, ErrMissingField},
		{"missing final", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"text":"x"}`, ErrMissingField},
		{"missing text", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true}`, ErrMissingField},
		{"null segment", `{"type":"CAPTION_UPDATE","segment":null,"revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrMissingField},
		{"null final", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":null,"text":"x"}`, ErrMissingField},
		{"null text", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true,"text":null}`, ErrMissingField},

		{"segment string", `{"type":"CAPTION_UPDATE","segment":"1","revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrBadType},
		{"segment fraction", `{"type":"CAPTION_UPDATE","segment":1.5,"revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrBadType},
		{"revision bool", `{"type":"CAPTION_UPDATE","segment":1,"revision":true,"source_ts":1,"final":true,"text":"x"}`, ErrBadType},
		{"source_ts string", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":"1","final":true,"text":"x"}`, ErrBadType},
		{"final string", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":"true","text":"x"}`, ErrBadType},
		{"final number", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":1,"text":"x"}`, ErrBadType},
		{"text number", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true,"text":5}`, ErrBadType},
		{"text object", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true,"text":{}}`, ErrBadType},

		{"segment zero", `{"type":"CAPTION_UPDATE","segment":0,"revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrOutOfRange},
		{"segment negative", `{"type":"CAPTION_UPDATE","segment":-3,"revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrOutOfRange},
		{"revision zero", `{"type":"CAPTION_UPDATE","segment":1,"revision":0,"source_ts":1,"final":true,"text":"x"}`, ErrOutOfRange},
		{"revision negative", `{"type":"CAPTION_UPDATE","segment":1,"revision":-1,"source_ts":1,"final":true,"text":"x"}`, ErrOutOfRange},
		{"revision string", `{"type":"CAPTION_UPDATE","segment":1,"revision":"1","source_ts":1,"final":true,"text":"x"}`, ErrBadType},
		{"source_ts bool", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":true,"final":true,"text":"x"}`, ErrBadType},
		{"segment overflow", `{"type":"CAPTION_UPDATE","segment":99999999999999999999,"revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrOutOfRange},
		{"segment huge exponent", `{"type":"CAPTION_UPDATE","segment":1e300,"revision":1,"source_ts":1,"final":true,"text":"x"}`, ErrOutOfRange},
		{"source_ts zero", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":0,"final":true,"text":"x"}`, ErrOutOfRange},
		{"source_ts negative", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":-1,"final":true,"text":"x"}`, ErrOutOfRange},
		{"source_ts overflow", `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1e999,"final":true,"text":"x"}`, ErrOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := DecodeSourceMessage([]byte(tt.raw))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if msg != nil {
				t.Errorf("msg = %v, want nil on error", msg)
			}
		})
	}
}

func TestDecodeSourceMessageSourceDone(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want SourceDone
	}{
		{
			name: "full",
			raw:  `{"type":"SOURCE_DONE","session":"L1","source_ts":1760000060.5,"segments":80}`,
			want: SourceDone{Type: TypeSourceDone, Session: "L1", SourceTS: 1760000060.5, Segments: 80},
		},
		{
			name: "unknown fields ignored",
			raw:  `{"type":"SOURCE_DONE","segments":5,"phase":"end"}`,
			want: SourceDone{Type: TypeSourceDone, Segments: 5},
		},
		{
			name: "bare",
			raw:  `{"type":"SOURCE_DONE"}`,
			want: SourceDone{Type: TypeSourceDone},
		},
		{
			name: "bad fields become zero",
			raw:  `{"type":"SOURCE_DONE","session":1,"source_ts":"x","segments":1.5}`,
			want: SourceDone{Type: TypeSourceDone},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := DecodeSourceMessage([]byte(tt.raw))
			if err != nil {
				t.Fatalf("DecodeSourceMessage: %v", err)
			}
			if got := *msg.(*SourceDone); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDecodeHello(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want any
	}{
		{
			name: "source hello",
			raw:  `{"type":"SOURCE_HELLO","session":"L1"}`,
			want: SourceHello{Type: TypeSourceHello, Session: "L1"},
		},
		{
			name: "source hello unknown fields ignored",
			raw:  `{"type":"SOURCE_HELLO","session":"L1","version":2}`,
			want: SourceHello{Type: TypeSourceHello, Session: "L1"},
		},
		{
			name: "source hello bare",
			raw:  `{"type":"SOURCE_HELLO"}`,
			want: SourceHello{Type: TypeSourceHello},
		},
		{
			name: "client hello from spec",
			raw:  `{"type":"CLIENT_HELLO","client_id":17,"session":"L1","last_segment":38,"last_revision":3,"history_seconds":30}`,
			want: ClientHello{Type: TypeClientHello, ClientID: 17, Session: "L1", LastSegment: 38, LastRevision: 3, HistorySeconds: 30},
		},
		{
			name: "client hello new client",
			raw:  `{"type":"CLIENT_HELLO","client_id":1,"session":"L1","last_segment":0,"last_revision":0,"history_seconds":30}`,
			want: ClientHello{Type: TypeClientHello, ClientID: 1, Session: "L1", HistorySeconds: 30},
		},
		{
			name: "client hello bare defaults to zero",
			raw:  `{"type":"CLIENT_HELLO"}`,
			want: ClientHello{Type: TypeClientHello},
		},
		{
			name: "client hello unknown fields and bad informational fields",
			raw:  `{"type":"CLIENT_HELLO","client_id":"abc","session":[],"ua":"firefox","history_seconds":15.5}`,
			want: ClientHello{Type: TypeClientHello, HistorySeconds: 15.5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := DecodeHello([]byte(tt.raw))
			if err != nil {
				t.Fatalf("DecodeHello: %v", err)
			}
			var got any
			switch m := msg.(type) {
			case *SourceHello:
				got = *m
			case *ClientHello:
				got = *m
			default:
				t.Fatalf("got %T", msg)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDecodeHelloClampsHistorySeconds(t *testing.T) {
	tests := []struct {
		raw  string
		want float64
	}{
		{`-5`, 0},
		{`0`, 0},
		{`30`, 30},
		{`60`, 60},
		{`61`, 60},
		{`999`, 60},
		{`1e300`, 60},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			msg, err := DecodeHello([]byte(`{"type":"CLIENT_HELLO","history_seconds":` + tt.raw + `}`))
			if err != nil {
				t.Fatalf("DecodeHello: %v", err)
			}
			if got := msg.(*ClientHello).HistorySeconds; got != tt.want {
				t.Errorf("HistorySeconds = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeHelloRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"not json", `hello`, ErrMalformed},
		{"binary garbage", "\x00\xff\xfe", ErrMalformed},
		{"array", `[]`, ErrMalformed},
		{"no type", `{"session":"L1"}`, ErrUnknownType},
		{"lowercase type", `{"type":"client_hello"}`, ErrUnknownType},
		{"caption as hello", specCaption, ErrUnknownType},
		{"history as hello", `{"type":"HISTORY","items":[]}`, ErrUnknownType},
		{"last_segment string", `{"type":"CLIENT_HELLO","last_segment":"38"}`, ErrBadType},
		{"last_revision fraction", `{"type":"CLIENT_HELLO","last_revision":2.5}`, ErrBadType},
		{"history_seconds string", `{"type":"CLIENT_HELLO","history_seconds":"30"}`, ErrBadType},
		{"last_segment negative", `{"type":"CLIENT_HELLO","last_segment":-1}`, ErrOutOfRange},
		{"last_revision negative", `{"type":"CLIENT_HELLO","last_revision":-1}`, ErrOutOfRange},
		{"history_seconds overflow", `{"type":"CLIENT_HELLO","history_seconds":1e999}`, ErrOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := DecodeHello([]byte(tt.raw))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if msg != nil {
				t.Errorf("msg = %v, want nil on error", msg)
			}
		})
	}
}

func TestErrorsNameField(t *testing.T) {
	_, err := DecodeSourceMessage([]byte(`{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true}`))
	if err == nil || err.Error() != "protocol: required field missing: text" {
		t.Errorf("err = %v, want it to name the text field", err)
	}
}
