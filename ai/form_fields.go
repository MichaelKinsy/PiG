package ai

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// formFields is the Record<string, string> that Pi passes to `new URLSearchParams(fields)`: name/value pairs in insertion order,
// serialized by the WHATWG application/x-www-form-urlencoded serializer. url.Values.Encode sorts the keys and uses QueryEscape,
// which differs on '*' and '~'.
type formFields [][2]string

// Encode is URLSearchParams.toString().
func (fields formFields) Encode() string {
	var out strings.Builder
	for index, field := range fields {
		if index > 0 {
			out.WriteByte('&')
		}
		out.WriteString(nodeurl.FormURLEncode(field[0]))
		out.WriteByte('=')
		out.WriteString(nodeurl.FormURLEncode(field[1]))
	}
	return out.String()
}
