package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// jsonObject is a decoded JSON object whose values are still raw. Decoding
// into a map rather than a struct is what lets us tell a missing field from a
// zero, and a wrong JSON type from a right one. Keys we never look up are the
// unknown fields, and they are ignored by construction.
type jsonObject map[string]json.RawMessage

// parseObject decodes rawMessage as a JSON object. Anything else, including a
// top-level null, is ErrMalformed.
func parseObject(rawMessage []byte) (jsonObject, error) {
	var message jsonObject
	if parseErr := json.Unmarshal(rawMessage, &message); parseErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, parseErr)
	}
	if message == nil {
		return nil, fmt.Errorf("%w: not a JSON object", ErrMalformed)
	}
	return message, nil
}

// messageType returns the "type" field. A missing or non-string type is
// ErrUnknownType, since nothing about the message can be interpreted.
func (message jsonObject) messageType() (string, error) {
	rawType, found := message.lookup("type")
	if !found {
		return "", fmt.Errorf("%w: no type field", ErrUnknownType)
	}
	typeName, isString := parseString(rawType)
	if !isString {
		return "", fmt.Errorf("%w: type is not a string", ErrUnknownType)
	}
	return typeName, nil
}

// lookup returns the raw value of fieldName. A JSON null counts as absent.
func (message jsonObject) lookup(fieldName string) (json.RawMessage, bool) {
	rawValue, found := message[fieldName]
	if !found || string(rawValue) == "null" {
		return nil, false
	}
	return rawValue, true
}

// require returns the raw value of fieldName, or ErrMissingField if it is
// absent.
func (message jsonObject) require(fieldName string) (json.RawMessage, error) {
	rawValue, found := message.lookup(fieldName)
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrMissingField, fieldName)
	}
	return rawValue, nil
}

// requiredInt returns fieldName as an int64, failing if it is absent.
func (message jsonObject) requiredInt(fieldName string) (int64, error) {
	rawValue, missingErr := message.require(fieldName)
	if missingErr != nil {
		return 0, missingErr
	}
	return parseInt(fieldName, rawValue)
}

// optionalInt returns fieldName as an int64, or defaultValue if it is absent.
func (message jsonObject) optionalInt(fieldName string, defaultValue int64) (int64, error) {
	rawValue, found := message.lookup(fieldName)
	if !found {
		return defaultValue, nil
	}
	return parseInt(fieldName, rawValue)
}

// requiredFloat returns fieldName as a float64, failing if it is absent.
func (message jsonObject) requiredFloat(fieldName string) (float64, error) {
	rawValue, missingErr := message.require(fieldName)
	if missingErr != nil {
		return 0, missingErr
	}
	return parseFloat(fieldName, rawValue)
}

// optionalFloat returns fieldName as a float64, or defaultValue if it is
// absent.
func (message jsonObject) optionalFloat(fieldName string, defaultValue float64) (float64, error) {
	rawValue, found := message.lookup(fieldName)
	if !found {
		return defaultValue, nil
	}
	return parseFloat(fieldName, rawValue)
}

// requiredBool returns fieldName as a bool, failing if it is absent.
func (message jsonObject) requiredBool(fieldName string) (bool, error) {
	rawValue, missingErr := message.require(fieldName)
	if missingErr != nil {
		return false, missingErr
	}
	switch string(rawValue) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%w: %s must be a boolean", ErrBadType, fieldName)
}

// requiredString returns fieldName as a string, failing if it is absent.
func (message jsonObject) requiredString(fieldName string) (string, error) {
	rawValue, missingErr := message.require(fieldName)
	if missingErr != nil {
		return "", missingErr
	}
	text, isString := parseString(rawValue)
	if !isString {
		return "", fmt.Errorf("%w: %s must be a string", ErrBadType, fieldName)
	}
	return text, nil
}

// informationalString returns fieldName as a string, or "" if it is absent or
// not a string. Used for fields we accept but never act on, so a bad value
// there can never cost us a message.
func (message jsonObject) informationalString(fieldName string) string {
	rawValue, found := message.lookup(fieldName)
	if !found {
		return ""
	}
	text, _ := parseString(rawValue)
	return text
}

// informationalInt is informationalString for integers.
func (message jsonObject) informationalInt(fieldName string) int64 {
	number, parseErr := message.optionalInt(fieldName, 0)
	if parseErr != nil {
		return 0
	}
	return number
}

// informationalFloat is informationalString for numbers.
func (message jsonObject) informationalFloat(fieldName string) float64 {
	number, parseErr := message.optionalFloat(fieldName, 0)
	if parseErr != nil {
		return 0
	}
	return number
}

// parseInt reads a JSON number that must be a whole number. 42 and 42.0 are
// both accepted; "42", 42.5 and true are ErrBadType; values outside int64 are
// ErrOutOfRange.
func parseInt(fieldName string, rawValue json.RawMessage) (int64, error) {
	if !isNumber(rawValue) {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrBadType, fieldName)
	}
	digits := string(rawValue)
	integer, intErr := strconv.ParseInt(digits, 10, 64)
	if intErr == nil {
		return integer, nil
	}
	if errors.Is(intErr, strconv.ErrRange) {
		return 0, fmt.Errorf("%w: %s overflows int64", ErrOutOfRange, fieldName)
	}
	// Not plain integer syntax (has '.' or an exponent): accept it only if it
	// is a whole number that fits. ParseFloat only fails here on overflow,
	// since the JSON parser has already checked the syntax.
	float, floatErr := strconv.ParseFloat(digits, 64)
	if floatErr != nil {
		return 0, fmt.Errorf("%w: %s overflows int64", ErrOutOfRange, fieldName)
	}
	if float != math.Trunc(float) {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrBadType, fieldName)
	}
	if float < -(1<<63) || float >= 1<<63 {
		return 0, fmt.Errorf("%w: %s overflows int64", ErrOutOfRange, fieldName)
	}
	return int64(float), nil
}

// parseFloat reads a JSON number as a finite float64. JSON cannot spell NaN
// or Infinity, so the only non-finite result is overflow, which ParseFloat
// reports as an error.
func parseFloat(fieldName string, rawValue json.RawMessage) (float64, error) {
	if !isNumber(rawValue) {
		return 0, fmt.Errorf("%w: %s must be a number", ErrBadType, fieldName)
	}
	number, floatErr := strconv.ParseFloat(string(rawValue), 64)
	if floatErr != nil {
		return 0, fmt.Errorf("%w: %s is not a finite number", ErrOutOfRange, fieldName)
	}
	return number, nil
}

// isNumber reports whether rawValue is a JSON number. rawValue has already
// passed the JSON parser, so checking the first byte is enough.
func isNumber(rawValue json.RawMessage) bool {
	if len(rawValue) == 0 {
		return false
	}
	firstByte := rawValue[0]
	return firstByte == '-' || (firstByte >= '0' && firstByte <= '9')
}

// parseString reads a JSON string value. It reports false for any other
// JSON type, so a number is never silently converted to text.
func parseString(rawValue json.RawMessage) (string, bool) {
	if len(rawValue) == 0 || rawValue[0] != '"' {
		return "", false
	}
	var text string
	if decodeErr := json.Unmarshal(rawValue, &text); decodeErr != nil {
		return "", false
	}
	return text, true
}
