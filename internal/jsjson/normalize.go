// Package jsjson reads JSON the way JSON.parse and property access do where encoding/json differs.
package jsjson

import (
	"math"
	"strconv"
)

// NormalizeIntegers rewrites each number literal that JavaScript reads as an integer but encoding/json refuses for an integer target (12.0, 1.2e1, 12E0, 120e-1, -0.0e0) to its plain integer text, and leaves every other byte alone. It returns data itself, without allocating, when nothing needs rewriting. A literal is rewritten only when its value is an exact integer below 2^53 in magnitude; strings are skipped.
func NormalizeIntegers(data []byte) []byte {
	var out []byte
	copied := 0
	for i := 0; i < len(data); {
		switch c := data[i]; {
		case c == '"':
			i = skipString(data, i)
		case c == '-' || (c >= '0' && c <= '9'):
			end := numberEnd(data, i)
			if text, rewrite := integerText(data[i:end]); rewrite {
				if out == nil {
					out = make([]byte, 0, len(data))
				}
				out = append(out, data[copied:i]...)
				out = append(out, text...)
				copied = end
			}
			i = end
		default:
			i++
		}
	}
	if out == nil {
		return data
	}
	return append(out, data[copied:]...)
}

func skipString(data []byte, start int) int {
	for i := start + 1; i < len(data); i++ {
		switch data[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(data)
}

func numberEnd(data []byte, start int) int {
	i := start
	for i < len(data) {
		switch c := data[i]; {
		case c >= '0' && c <= '9', c == '-', c == '+', c == '.', c == 'e', c == 'E':
			i++
		default:
			return i
		}
	}
	return i
}

// integerText returns the plain integer spelling of literal when it has a fraction or exponent part yet denotes an exact integer.
func integerText(literal []byte) (string, bool) {
	plain := true
	for _, c := range literal {
		if c == '.' || c == 'e' || c == 'E' {
			plain = false
			break
		}
	}
	if plain {
		return "", false
	}
	if !validNumber(literal) {
		return "", false
	}
	value, err := strconv.ParseFloat(string(literal), 64)
	if err != nil || value != math.Trunc(value) || math.Abs(value) >= 1<<53 {
		return "", false
	}
	if value == 0 {
		return "0", true
	}
	return strconv.FormatInt(int64(value), 10), true
}

// validNumber reports whether literal is a JSON number: -?(0|[1-9][0-9]*)(.[0-9]+)?([eE][+-]?[0-9]+)?. A malformed literal stays as written so the decoder refuses it as JSON.parse does.
func validNumber(literal []byte) bool {
	i := 0
	if i < len(literal) && literal[i] == '-' {
		i++
	}
	digits := func() int {
		start := i
		for i < len(literal) && literal[i] >= '0' && literal[i] <= '9' {
			i++
		}
		return i - start
	}
	switch {
	case i < len(literal) && literal[i] == '0':
		i++
	case digits() == 0:
		return false
	}
	if i < len(literal) && literal[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	}
	if i < len(literal) && (literal[i] == 'e' || literal[i] == 'E') {
		i++
		if i < len(literal) && (literal[i] == '+' || literal[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(literal)
}
