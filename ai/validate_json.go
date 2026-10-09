package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

// stringifyArgumentJSON mirrors JSON.stringify for the received JSON value,
// retaining object order without HTML escaping or rewriting literal backslashes.
func stringifyArgumentJSON(raw json.RawMessage) ([]byte, error) {
	schema, err := parseArgumentSchema(raw)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	switch value := schema.doc.(type) {
	case map[string]any:
		out.WriteByte('{')
		keys := argumentObjectKeys(schema.keys)
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(quoteArgumentString(key))
			out.WriteByte(':')
			child, err := stringifyArgumentJSON(schema.fields[key])
			if err != nil {
				return nil, err
			}
			out.Write(child)
		}
		out.WriteByte('}')
	case []any:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		out.WriteByte('[')
		for i, item := range items {
			if i > 0 {
				out.WriteByte(',')
			}
			child, err := stringifyArgumentJSON(item)
			if err != nil {
				return nil, err
			}
			out.Write(child)
		}
		out.WriteByte(']')
	case string:
		out.WriteString(quoteArgumentString(value))
	case float64:
		out.WriteString(argumentNumberString(value))
	case bool:
		out.WriteString(strconv.FormatBool(value))
	case nil:
		out.WriteString("null")
	}
	return out.Bytes(), nil
}

func argumentObjectKeys(keys []string) []string {
	var indices []uint64
	var names []string
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		index, err := strconv.ParseUint(key, 10, 32)
		if err == nil && index < 1<<32-1 && strconv.FormatUint(index, 10) == key {
			indices = append(indices, index)
		} else {
			names = append(names, key)
		}
	}
	slices.Sort(indices)
	result := make([]string, 0, len(indices))
	for _, index := range indices {
		result = append(result, strconv.FormatUint(index, 10))
	}
	return append(result, names...)
}

func quoteArgumentString(value string) string {
	var out bytes.Buffer
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}
