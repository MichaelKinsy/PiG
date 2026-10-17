package env

import (
	"fmt"
	"strings"
)

// jsonString is JSON.stringify of a string, for error messages: it escapes only `"`, `\` and control characters, and
// keeps U+2028 and U+2029, which encoding/json escapes. Bytes that are not UTF-8 become U+FFFD, as Node decodes them.
func jsonString(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if r < 0x20 {
				_, _ = fmt.Fprintf(&builder, `\u%04x`, r)
			} else {
				builder.WriteRune(r)
			}
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
