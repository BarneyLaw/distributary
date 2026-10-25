package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// object is a decoded JSON object whose values are still raw. Decoding into a
// map rather than a struct is what lets us tell a missing field from a zero,
// and a wrong JSON type from a right one. Keys we never look up are the
// unknown fields, and they are ignored by construction.
type object map[string]json.RawMessage

// parseObject decodes raw as a JSON object. Anything else, including a
// top-level null, is ErrMalformed.
func parseObject(raw []byte) (object, error) {
	var obj object
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("%w: not a JSON object", ErrMalformed)
	}
	return obj, nil
}

// messageType returns the "type" field. A missing or non-string type is
// ErrUnknownType, since nothing about the message can be interpreted.
func (o object) messageType() (string, error) {
	v, ok := o.lookup("type")
	if !ok {
		return "", fmt.Errorf("%w: no type field", ErrUnknownType)
	}
	var t string
	if !isString(v) || json.Unmarshal(v, &t) != nil {
		return "", fmt.Errorf("%w: type is not a string", ErrUnknownType)
	}
	return t, nil
}

// lookup returns the raw value for key. A JSON null counts as absent.
func (o object) lookup(key string) (json.RawMessage, bool) {
	v, ok := o[key]
	if !ok || string(v) == "null" {
		return nil, false
	}
	return v, true
}

// requiredInt returns key as an int64, failing if it is absent.
func (o object) requiredInt(key string) (int64, error) {
	v, ok := o.lookup(key)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	return parseInt(key, v)
}

// optionalInt returns key as an int64, or def if it is absent.
func (o object) optionalInt(key string, def int64) (int64, error) {
	v, ok := o.lookup(key)
	if !ok {
		return def, nil
	}
	return parseInt(key, v)
}

// requiredFloat returns key as a float64, failing if it is absent.
func (o object) requiredFloat(key string) (float64, error) {
	v, ok := o.lookup(key)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	return parseFloat(key, v)
}

// optionalFloat returns key as a float64, or def if it is absent.
func (o object) optionalFloat(key string, def float64) (float64, error) {
	v, ok := o.lookup(key)
	if !ok {
		return def, nil
	}
	return parseFloat(key, v)
}

// requiredBool returns key as a bool, failing if it is absent.
func (o object) requiredBool(key string) (bool, error) {
	v, ok := o.lookup(key)
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	switch string(v) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%w: %s must be a boolean", ErrBadType, key)
}

// requiredString returns key as a string, failing if it is absent.
func (o object) requiredString(key string) (string, error) {
	v, ok := o.lookup(key)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	var s string
	if !isString(v) || json.Unmarshal(v, &s) != nil {
		return "", fmt.Errorf("%w: %s must be a string", ErrBadType, key)
	}
	return s, nil
}

// informationalString returns key as a string, or "" if it is absent or not
// a string. Used for fields we accept but never act on, so a bad value there
// can never cost us a message.
func (o object) informationalString(key string) string {
	v, ok := o.lookup(key)
	if !ok {
		return ""
	}
	var s string
	if !isString(v) || json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

// informationalInt is informationalString for integers.
func (o object) informationalInt(key string) int64 {
	n, err := o.optionalInt(key, 0)
	if err != nil {
		return 0
	}
	return n
}

// informationalFloat is informationalString for numbers.
func (o object) informationalFloat(key string) float64 {
	f, err := o.optionalFloat(key, 0)
	if err != nil {
		return 0
	}
	return f
}

// parseInt reads a JSON number that must be a whole number. 42 and 42.0 are
// both accepted; "42", 42.5 and true are ErrBadType; values outside int64 are
// ErrOutOfRange.
func parseInt(key string, v json.RawMessage) (int64, error) {
	if !isNumber(v) {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrBadType, key)
	}
	s := string(v)
	n, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return n, nil
	}
	if errors.Is(err, strconv.ErrRange) {
		return 0, fmt.Errorf("%w: %s overflows int64", ErrOutOfRange, key)
	}
	// Not plain integer syntax (has '.' or an exponent): accept it only if it
	// is a whole number that fits.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%w: %s overflows int64", ErrOutOfRange, key)
	}
	if f != math.Trunc(f) {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrBadType, key)
	}
	if f < -(1<<63) || f >= 1<<63 {
		return 0, fmt.Errorf("%w: %s overflows int64", ErrOutOfRange, key)
	}
	return int64(f), nil
}

// parseFloat reads a JSON number as a finite float64.
func parseFloat(key string, v json.RawMessage) (float64, error) {
	if !isNumber(v) {
		return 0, fmt.Errorf("%w: %s must be a number", ErrBadType, key)
	}
	f, err := strconv.ParseFloat(string(v), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, fmt.Errorf("%w: %s is not a finite number", ErrOutOfRange, key)
	}
	return f, nil
}

// isNumber reports whether v is a JSON number. v has already passed the JSON
// parser, so checking the first byte is enough.
func isNumber(v json.RawMessage) bool {
	return len(v) > 0 && (v[0] == '-' || (v[0] >= '0' && v[0] <= '9'))
}

// isString reports whether v is a JSON string.
func isString(v json.RawMessage) bool {
	return len(v) > 0 && v[0] == '"'
}
