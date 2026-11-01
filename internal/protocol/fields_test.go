package protocol

import (
	"encoding/json"
	"testing"
)

// These call the field helpers directly to cover inputs the JSON parser
// never passes through DecodeHello or DecodeSourceMessage.

func TestIsNumber(t *testing.T) {
	testCases := []struct {
		rawValue string
		want     bool
	}{
		{``, false},
		{`0`, true},
		{`9`, true},
		{`-1`, true},
		{`"1"`, false},
		{`true`, false},
		{`[]`, false},
	}
	for _, testCase := range testCases {
		if got := isNumber(json.RawMessage(testCase.rawValue)); got != testCase.want {
			t.Errorf("isNumber(%q) = %v, want %v", testCase.rawValue, got, testCase.want)
		}
	}
}

func TestParseString(t *testing.T) {
	testCases := []struct {
		name         string
		rawValue     string
		wantText     string
		wantIsString bool
	}{
		{"string", `"hi"`, "hi", true},
		{"empty input", ``, "", false},
		{"number", `5`, "", false},
		{"unterminated string", `"hi`, "", false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			text, isString := parseString(json.RawMessage(testCase.rawValue))
			if text != testCase.wantText || isString != testCase.wantIsString {
				t.Errorf("parseString(%q) = %q, %v, want %q, %v",
					testCase.rawValue, text, isString, testCase.wantText, testCase.wantIsString)
			}
		})
	}
}
