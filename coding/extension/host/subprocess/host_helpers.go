package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/yaml12"
)

// ParseFrontmatterJSON is Pi's parseFrontmatter over the host's parser: the JSON of the parsed header (see ParseFrontmatterResult) and the body. A
// malformed header is the error parseFrontmatter throws. The CallParseFrontmatter handler and the fused extensions' SDK hook share it.
func ParseFrontmatterJSON(content string) (frontmatterJSON []byte, body string, err error) {
	doc := frontmatter.ParseOrdered(content)
	if doc.Err != nil {
		return nil, "", doc.Err
	}
	var out strings.Builder
	if err := writeYAMLValue(&out, doc.Value); err != nil {
		return nil, "", err
	}
	return []byte(out.String()), doc.Body, nil
}

// ParseDateMs is `Date.parse(text)` through the host's port of V8's parser: the time value in milliseconds, false for NaN.
func ParseDateMs(text string) (float64, bool) {
	ms := ai.DateParse(text)
	return ms, !math.IsNaN(ms)
}

// handleParseFrontmatter serves CallParseFrontmatter.
func (h *Host) handleParseFrontmatter(call *CallPayload) (*CallResultPayload, error) {
	var args ParseFrontmatterArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	frontmatterJSON, body, err := ParseFrontmatterJSON(args.Content)
	if err != nil {
		return nil, err
	}
	// Written by hand: json.Marshal would rewrite the frontmatter's < > & and U+2028 as escapes, where JSON.stringify leaves them.
	data := append([]byte(`{"frontmatter":`), frontmatterJSON...)
	data = append(data, `,"body":`...)
	data = append(data, quoteJS(body)...)
	data = append(data, '}')
	return &CallResultPayload{Result: data}, nil
}

// handleParseDate serves CallParseDate.
func (h *Host) handleParseDate(call *CallPayload) (*CallResultPayload, error) {
	var args ParseDateArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	var result ParseDateResult
	if ms, ok := ParseDateMs(args.Text); ok {
		result.Ms = &ms
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

// writeYAMLValue is JSON.stringify of a value yaml12.ParseOrdered built: an object lists its array-index keys first, ascending, then the others in
// insertion order, and a non-finite number is null. A value only an explicit YAML tag builds (a set, a map, a date, a symbol, binary data) is an error here.
func writeYAMLValue(out *strings.Builder, value any) error {
	switch value := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(value))
	case float64:
		switch {
		case math.IsNaN(value) || math.IsInf(value, 0):
			out.WriteString("null")
		case value == 0:
			out.WriteString("0")
		default:
			out.WriteString(jsnumber.String(value))
		}
	case string:
		out.WriteString(quoteJS(value))
	case []any:
		out.WriteByte('[')
		for i, element := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeYAMLValue(out, element); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		// ParseOrdered builds only *yaml12.Object; the plain map is the `{}` Pi's `parsed ?? {}` supplies for a header that is absent or empty.
		if len(value) != 0 {
			return errors.New("frontmatter holds an object whose key order is unknown")
		}
		out.WriteString("{}")
	case *yaml12.Object:
		out.WriteByte('{')
		for i, key := range javaScriptKeyOrder(value.Keys) {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quoteJS(key))
			out.WriteByte(':')
			if err := writeYAMLValue(out, value.Values[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return errors.New("frontmatter holds a value only a YAML tag builds (" + fmt.Sprintf("%T", value) + ")")
	}
	return nil
}

// javaScriptKeyOrder is the order Object.keys gives: the array-index keys ("0" up to 4294967294) ascending, then the other keys in insertion order.
func javaScriptKeyOrder(keys []string) []string {
	var indexes, others []string
	for _, key := range keys {
		if number, err := strconv.ParseUint(key, 10, 64); err == nil && number <= math.MaxUint32-1 && strconv.FormatUint(number, 10) == key {
			indexes = append(indexes, key)
		} else {
			others = append(others, key)
		}
	}
	slices.SortFunc(indexes, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
	return append(indexes, others...)
}

// quoteJS is QuoteJSONString: a quote, a backslash and the control characters are escaped (\b \f \n \r \t short, the rest as lowercase \u00xx) and every other
// character, including < > & U+2028 and U+2029, is written as it is.
func quoteJS(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, c := range text {
		switch {
		case c == '"':
			out.WriteString(`\"`)
		case c == '\\':
			out.WriteString(`\\`)
		case c == '\b':
			out.WriteString(`\b`)
		case c == '\f':
			out.WriteString(`\f`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c == '\t':
			out.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(&out, `\u%04x`, c)
		default:
			out.WriteRune(c)
		}
	}
	out.WriteByte('"')
	return out.String()
}
