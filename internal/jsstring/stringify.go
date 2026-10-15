package jsstring

import (
	"bytes"
	"encoding"
	"reflect"
	"sync"
	"unicode/utf8"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
)

// MarshalJSON encodes value as JSON.stringify(value) does (numbers too: a non-finite number is null and negative zero is 0), retaining unmatched UTF-16 units as surrogate escapes and writing <, >, & and the line and paragraph separators literally and negative zero as 0 even inside custom marshalers. Escaped backslashes remain escaped. The encoder writes that text in one pass (json.MarshalStringify); a value that implements json.StringifyAppender appends its own text.
func MarshalJSON(value any) ([]byte, error) { return json.MarshalStringify(value) }

// AppendMarshalJSON appends MarshalJSON(value) to dst, encoding into dst itself. On an error it returns dst with its original length.
func AppendMarshalJSON(dst []byte, value any) ([]byte, error) {
	return json.AppendStringify(dst, value)
}

// maxPooledLengthBuffer bounds the buffer StringifyLength keeps between calls (golang/go#23199).
const maxPooledLengthBuffer = 64 << 10

var lengthBuffers = sync.Pool{New: func() any { return new([]byte) }}

// StringifyLength returns the number of UTF-16 units in JSON.stringify(value) and encodes without keeping the text. Ill-formed UTF-8 is U+FFFD in a JavaScript string, one unit per maximal subpart, which the encoder writes raw as JSON.stringify does.
// A slice the encoder writes element by element is counted the same way, "[", the elements joined by ",", "]", each encoded on its own: the
// pooled buffer then holds one element rather than the whole array, so counting a long list of tools does not allocate its whole text.
func StringifyLength(value any) (int, error) {
	if v := reflect.ValueOf(value); v.Kind() == reflect.Slice && v.Len() > 1 && encodesElementwise(v.Type()) {
		length := v.Len() + 1
		for i := range v.Len() {
			// A slice element is addressable, so the encoder uses its pointer methods; the element's pointer encodes as the element does.
			n, err := stringifyLength(v.Index(i).Addr().Interface())
			if err != nil {
				return 0, err
			}
			length += n
		}
		return length, nil
	}
	return stringifyLength(value)
}

var (
	marshalerType     = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// encodesElementwise reports whether the encoder writes a slice of type t as its elements in brackets: not a byte slice (base64) and not
// a type that marshals itself.
func encodesElementwise(t reflect.Type) bool {
	if t.Elem().Kind() == reflect.Uint8 {
		return false
	}
	p := reflect.PointerTo(t)
	return !t.Implements(marshalerType) && !p.Implements(marshalerType) && !t.Implements(textMarshalerType) && !p.Implements(textMarshalerType)
}

// stringifyLength is StringifyLength of value encoded whole.
func stringifyLength(value any) (int, error) {
	buffer := lengthBuffers.Get().(*[]byte)
	defer func() {
		if cap(*buffer) > maxPooledLengthBuffer {
			*buffer = nil
		}
		lengthBuffers.Put(buffer)
	}()
	text, err := json.AppendStringify((*buffer)[:0], value)
	*buffer = text
	if err != nil {
		return 0, err
	}
	return utf16Length(text), nil
}

// utf16Length counts the UTF-16 units of JSON text the encoder wrote. That text is UTF-8: the encoder writes an unmatched surrogate as an escape and ill-formed UTF-8 as U+FFFD, so no raw surrogate half reaches the count (unlike Length, which reads strings that may hold them).
func utf16Length(text []byte) int {
	count := 0
	for len(text) > 0 {
		r, size := utf8.DecodeRune(text)
		text = text[size:]
		count++
		if r > 0xffff {
			count++
		}
	}
	return count
}

// AppendString appends JSON.stringify(s) (jsparity.AppendString): a WTF-8 surrogate half prints as its escape unless it is the first half of a pair
// whose second half follows, and ill-formed UTF-8 prints as U+FFFD, the character a decoded JavaScript string holds.
func AppendString(dst []byte, s string) []byte { return jsparity.AppendString(dst, s) }

// UnescapeForStringify rewrites the escapes encoding/json writes and JSON.stringify does not, \u003c, \u003e, \u0026, \u2028 and \u2029,
// back to the characters they stand for. An escaped backslash before such text is left alone. It returns encoded itself when it holds none
// of them, so the common case does not copy; otherwise encoded is left as it is and the result is a new slice.
func UnescapeForStringify(encoded []byte) []byte {
	if !hasStringifyEscape(encoded) {
		return encoded
	}
	return unescapeInPlace(bytes.Clone(encoded))
}

func hasStringifyEscape(encoded []byte) bool {
	return bytes.Contains(encoded, []byte(`\u00`)) || bytes.Contains(encoded, []byte(`\u202`))
}

// UnescapeForStringifyInPlace is UnescapeForStringify written over encoded itself: each replacement is shorter than the escape it
// stands for, so the output never passes the text it reads. The result is encoded, shortened, and encoded's later bytes are undefined.
func UnescapeForStringifyInPlace(encoded []byte) []byte {
	if !hasStringifyEscape(encoded) {
		return encoded
	}
	return unescapeInPlace(encoded)
}

func unescapeInPlace(encoded []byte) []byte {
	read, write := 0, 0
	for {
		// The text up to the next backslash holds no escape: it is moved as one span.
		index := bytes.IndexByte(encoded[read:], '\\')
		if index < 0 || read+index+1 >= len(encoded) {
			write += copy(encoded[write:], encoded[read:])
			return encoded[:write]
		}
		write += copy(encoded[write:], encoded[read:read+index])
		read += index
		escape := encoded[read:]
		if len(escape) >= 6 && escape[1] == 'u' {
			var replacement string
			switch string(escape[:6]) {
			case `\u003c`, `\u003C`:
				replacement = "<"
			case `\u003e`, `\u003E`:
				replacement = ">"
			case `\u0026`:
				replacement = "&"
			case `\u2028`:
				replacement = "\u2028"
			case `\u2029`:
				replacement = "\u2029"
			}
			if replacement != "" {
				write += copy(encoded[write:], replacement)
				read += 6
				continue
			}
		}
		encoded[write], encoded[write+1] = escape[0], escape[1]
		write += 2
		read += 2
	}
}
