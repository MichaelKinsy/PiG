package jsstring

import (
	"bytes"
	stdjson "encoding/json"
	"strings"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A Go string holds a lone UTF-16 surrogate, a unit Pi's strings can hold, as three WTF-8 bytes. encoding/json writes those
// bytes as three U+FFFD and reads the escape `\ud83d` as U+FFFD; Pi's JSON.stringify writes the unit as the escape and
// JSON.parse reads it back. The two functions below keep encoding/json's work, bytes and errors for every text without a
// lone surrogate, and take the SDK's JavaScript string semantics only for the text that has one, so the common case costs
// what it did.

// replacementBytes is U+FFFD as encoding/json writes it for an ill-formed byte.
var replacementBytes = []byte("\xef\xbf\xbd")

// MarshalKeepingSurrogates is encoding/json's Marshal, except that a string with a lone surrogate comes out as the escape
// JSON.stringify writes (`\ud83d`) instead of U+FFFD. A value that is not JSON (NaN) is the same error as encoding/json's.
func MarshalKeepingSurrogates(value any) ([]byte, error) {
	encoded, err := stdjson.Marshal(value)
	if err != nil {
		return nil, err
	}
	// encoding/json turns each ill-formed byte, so every byte of a lone surrogate, into U+FFFD: no replacement character, no loss.
	if !bytes.Contains(encoded, replacementBytes) {
		return encoded, nil
	}
	return sdkjson.Marshal(value)
}

// UnmarshalKeepingSurrogates is encoding/json's Unmarshal, except that the escape of a lone surrogate (`\ud83d`) is read as
// that unit, as JSON.parse does, instead of as U+FFFD. Its errors are encoding/json's. The body is not generic: the compiler
// makes one copy of a generic function for each shape of T, and every struct type is a shape of its own, so a body written
// over T was linked once per decoded struct (about 300 bytes each, 3,744 bytes in the stock binary).
func UnmarshalKeepingSurrogates[T any](data []byte, value *T) error {
	return unmarshalKeepingSurrogates(data, value)
}

func unmarshalKeepingSurrogates(data []byte, value any) error {
	if HasSurrogateEscape(data) && sdkjson.Unmarshal(data, value) == nil {
		return nil
	}
	// A text without such an escape, and one the other decoder refuses: the error is encoding/json's, the one a caller inspects, and value
	// holds what encoding/json read.
	return stdjson.Unmarshal(data, value)
}

// HasSurrogateEscape reports whether data holds the escape of a surrogate unit, `\ud800` to `\udfff`: the only text
// encoding/json reads differently from JSON.parse. A backslash that is itself escaped counts too; the caller then takes the
// slower decoder, which reads that text correctly as well.
func HasSurrogateEscape(data []byte) bool {
	for at := 0; ; {
		index := bytes.IndexByte(data[at:], '\\')
		if index < 0 {
			return false
		}
		at += index
		if at+3 < len(data) && data[at+1] == 'u' && (data[at+2] == 'd' || data[at+2] == 'D') {
			switch c := data[at+3]; {
			case c >= '8' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
				return true
			}
		}
		at++
	}
}

// HoldsSurrogate reports whether s holds a UTF-16 surrogate in its three-byte form (0xED, then a byte from 0xA0 to 0xBF): the text
// encoding/json writes as U+FFFD and JSON.stringify writes as an escape.
func HoldsSurrogate(s string) bool {
	for at := 0; ; at++ {
		index := strings.IndexByte(s[at:], 0xED)
		if index < 0 {
			return false
		}
		at += index
		if at+1 < len(s) && s[at+1] >= 0xA0 && s[at+1] <= 0xBF {
			return true
		}
	}
}
