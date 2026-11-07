// Package jsonstringify implements ECMAScript serialization for parsed JSON data.
package jsonstringify

// Ports packages/ai/src/utils/error-body.ts (safeJsonStringify for JSON input).

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
)

// Canonicalize applies JSON.parse/stringify semantics to validated JSON, preserving object insertion order and lone UTF-16 surrogates. Text that JSON.stringify would write unchanged is copied, without a decode and re-encode.
func Canonicalize(raw []byte) ([]byte, error) {
	return AppendCanonical(nil, raw)
}

// AppendCanonical appends Canonicalize(raw) to dst. Text that JSON.stringify writes unchanged is appended without an intermediate copy, so a caller that reuses dst allocates nothing for it. On an error dst is returned unchanged.
func AppendCanonical(dst, raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if IsCanonical(raw) {
		return append(dst, raw...), nil
	}
	canonical, err := canonicalize(raw)
	if err != nil {
		return dst, err
	}
	return append(dst, canonical...), nil
}

// canonicalize is the complete JSON.parse/stringify rewrite that Canonicalize skips for text IsCanonical accepts.
func canonicalize(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("empty JSON")
	}
	switch raw[0] {
	case '{':
		return object(raw)
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		out := []byte{'['}
		for i, value := range values {
			if i > 0 {
				out = append(out, ',')
			}
			encoded, err := canonicalize(value)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded...)
		}
		return append(out, ']'), nil
	case '"':
		return stringToken(raw), nil
	case 't', 'f', 'n':
		return raw, nil
	default:
		// JSON.parse reads a number too large for a double as Infinity, which JSON.stringify writes as null, and one too small as 0.
		value, err := strconv.ParseFloat(string(raw), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, err
		}
		return jsparity.AppendNumber(nil, value), nil
	}
}

type member struct {
	key   string
	value []byte
	index uint64
}

func object(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var members []member
	positions := map[string]int{}
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		key := string(stringToken(bytes.TrimLeft(raw[start:decoder.InputOffset()], ", \t\r\n")))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		encoded, err := canonicalize(value)
		if err != nil {
			return nil, err
		}
		if position, exists := positions[key]; exists {
			members[position].value = encoded
			continue
		}
		index := uint64(math.MaxUint32)
		name := key[1 : len(key)-1]
		if n, err := strconv.ParseUint(name, 10, 32); err == nil && strconv.FormatUint(n, 10) == name {
			index = n
		}
		positions[key] = len(members)
		members = append(members, member{key, encoded, index})
	}
	slices.SortStableFunc(members, func(a, b member) int {
		if a.index < b.index {
			return -1
		}
		if a.index > b.index {
			return 1
		}
		return 0
	})
	out := []byte{'{'}
	for i, entry := range members {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, entry.key...)
		out = append(out, ':')
		out = append(out, entry.value...)
	}
	return append(out, '}'), nil
}

// stringToken is JSON.stringify(JSON.parse(raw)) for a JSON string token: the string decodes as UTF-16 code units held in WTF-8 (a lone
// surrogate escape stays one unit) and jsparity.AppendString writes it.
func stringToken(raw []byte) []byte {
	if !bytes.ContainsRune(raw, '\\') && utf8.Valid(raw) {
		return raw
	}
	value, err := jv.Parse(raw)
	text, ok := value.(string)
	if err != nil || !ok {
		// The caller validated the token, so this is unreachable; keep the text rather than lose it.
		return raw
	}
	return jsparity.AppendString(nil, text)
}
