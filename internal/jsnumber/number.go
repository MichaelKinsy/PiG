// Package jsnumber implements ECMAScript numeric conversion and serialization for values that arrive as JSON.
package jsnumber

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

// FromJSON returns ToNumber of the value JSON.parse produces for raw. A nil or empty raw is undefined, which converts to NaN.
func FromJSON(raw []byte) float64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return math.NaN()
	}
	switch raw[0] {
	case 'n':
		return 0
	case 't':
		return 1
	case 'f':
		return 0
	case '"':
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return math.NaN()
		}
		return Parse(text)
	case '{':
		return math.NaN()
	case '[':
		text, ok := toPrimitiveString(raw)
		if !ok {
			return math.NaN()
		}
		return Parse(text)
	default:
		value, err := strconv.ParseFloat(string(raw), 64)
		if err != nil && !math.IsInf(value, 0) {
			return math.NaN()
		}
		return value
	}
}

// toPrimitiveString applies Array.prototype.toString (join with ",") to a JSON array.
func toPrimitiveString(raw []byte) (string, bool) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return "", false
	}
	parts := make([]string, len(values))
	for i, value := range values {
		value = bytes.TrimSpace(value)
		switch {
		case len(value) == 0 || value[0] == 'n':
			parts[i] = ""
		case value[0] == '"':
			if json.Unmarshal(value, &parts[i]) != nil {
				return "", false
			}
		case value[0] == '[':
			text, ok := toPrimitiveString(value)
			if !ok {
				return "", false
			}
			parts[i] = text
		case value[0] == '{':
			parts[i] = "[object Object]"
		case value[0] == 't' || value[0] == 'f':
			parts[i] = string(value)
		default:
			number, err := strconv.ParseFloat(string(value), 64)
			if err != nil && !math.IsInf(number, 0) {
				return "", false
			}
			parts[i] = String(number)
		}
	}
	return strings.Join(parts, ","), true
}

// Parse implements ECMAScript StringToNumber.
func Parse(text string) float64 {
	text = strings.TrimFunc(text, isWhitespace)
	if text == "" {
		return 0
	}
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			return parseInteger(text[2:], base)
		}
	}
	unsigned := text
	sign := 1.0
	switch text[0] {
	case '+':
		unsigned = text[1:]
	case '-':
		unsigned = text[1:]
		sign = -1
	}
	if unsigned == "Infinity" {
		return sign * math.Inf(1)
	}
	if !decimalLiteral(unsigned) {
		return math.NaN()
	}
	value, err := strconv.ParseFloat(unsigned, 64)
	if err != nil && !math.IsInf(value, 0) {
		return math.NaN()
	}
	return sign * value
}

func parseInteger(digits string, base int) float64 {
	for _, r := range digits {
		value := -1
		switch {
		case r >= '0' && r <= '9':
			value = int(r - '0')
		case r >= 'a' && r <= 'z':
			value = int(r-'a') + 10
		case r >= 'A' && r <= 'Z':
			value = int(r-'A') + 10
		}
		if value < 0 || value >= base {
			return math.NaN()
		}
	}
	integer, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return math.NaN()
	}
	value, _ := new(big.Float).SetInt(integer).Float64()
	return value
}

// decimalLiteral reports whether text matches StrUnsignedDecimalLiteral without Infinity.
func decimalLiteral(text string) bool {
	i, n := 0, len(text)
	intDigits := 0
	for i < n && text[i] >= '0' && text[i] <= '9' {
		i++
		intDigits++
	}
	fracDigits := 0
	if i < n && text[i] == '.' {
		i++
		for i < n && text[i] >= '0' && text[i] <= '9' {
			i++
			fracDigits++
		}
	}
	if intDigits == 0 && fracDigits == 0 {
		return false
	}
	if i < n && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < n && (text[i] == '+' || text[i] == '-') {
			i++
		}
		expDigits := 0
		for i < n && text[i] >= '0' && text[i] <= '9' {
			i++
			expDigits++
		}
		if expDigits == 0 {
			return false
		}
	}
	return i == n
}

// isWhitespace reports StrWhiteSpaceChar: WhiteSpace and LineTerminator.
func isWhitespace(r rune) bool {
	switch r {
	case '\t', '\v', '\f', ' ', 0xa0, 0xfeff, '\n', '\r', 0x2028, 0x2029:
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// String implements Number::toString for base 10.
func String(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	case value == 0:
		return "0"
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// JSON returns JSON.stringify(value): non-finite numbers serialize as null and -0 as 0.
func JSON(value float64) json.RawMessage {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return json.RawMessage("null")
	}
	return json.RawMessage(String(value))
}
