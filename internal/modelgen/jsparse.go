package modelgen

// JavaScript number parsing the sources use.

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// parseFloat reads the longest decimal prefix and ignores the rest.
var floatPrefix = regexp.MustCompile(`^[+-]?(?:Infinity|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)

const jsWhitespace = " \t\n\r\v\f\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// ParseFloat is Number.parseFloat(text): NaN when text has no decimal prefix.
func ParseFloat(text string) float64 {
	prefix := floatPrefix.FindString(strings.TrimLeft(text, jsWhitespace))
	switch prefix {
	case "":
		return math.NaN()
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	//portlint:allow numbers prefix is what parseFloat reads (floatPrefix is its grammar), which ParseFloat parses alike; an overflow is ±Inf as in JavaScript
	parsed, _ := strconv.ParseFloat(prefix, 64)
	return parsed
}

// stringify is String(value) for a parsed JSON value that is not an object: parseFloat of a number reads its decimal text.
func stringify(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return jsnumber.String(v)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return "null"
	}
	return "[object Object]"
}

func utf16Length(text string) int { return len(utf16.Encode([]rune(text))) }

// ownedSlice is `text.slice(0, end)` in UTF-16 code units; a split surrogate pair becomes U+FFFD.
func ownedSlice(text string, end int) string {
	units := utf16.Encode([]rune(text))
	return string(utf16.Decode(units[:end]))
}
