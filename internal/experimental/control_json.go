package experimental

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// canonicalControlJSON mirrors JSON.parse/stringify without losing object insertion order or lone surrogates.
// Input is validated JSON from encoding/json or the control reader, never an application-specific payload.
func canonicalControlJSON(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("empty control JSON")
	}
	switch raw[0] {
	case '{':
		return canonicalControlObject(raw)
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
			encoded, err := canonicalControlJSON(value)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded...)
		}
		return append(out, ']'), nil
	case '"':
		return canonicalControlString(raw), nil
	case 't', 'f', 'n':
		return raw, nil
	default:
		value, err := strconv.ParseFloat(string(raw), 64)
		if math.IsInf(value, 0) {
			return []byte("null"), nil
		}
		if err != nil {
			return nil, err
		}
		if value == 0 {
			return []byte("0"), nil
		}
		// encoding/json uses ECMAScript's fixed/exponent cutoffs for binary64 values.
		return json.Marshal(value)
	}
}

type controlJSONMember struct {
	key   string
	value []byte
	index uint64
}

func canonicalControlObject(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var members []controlJSONMember
	positions := make(map[string]int)
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		key := string(canonicalControlString(bytes.TrimLeft(raw[start:decoder.InputOffset()], ", \t\r\n")))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		encoded, err := canonicalControlJSON(value)
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
		members = append(members, controlJSONMember{key, encoded, index})
	}
	slices.SortStableFunc(members, func(a, b controlJSONMember) int {
		if a.index < b.index {
			return -1
		}
		if a.index > b.index {
			return 1
		}
		return 0
	})
	out := []byte{'{'}
	for i, member := range members {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, member.key...)
		out = append(out, ':')
		out = append(out, member.value...)
	}
	return append(out, '}'), nil
}

func canonicalControlString(raw []byte) []byte {
	if !bytes.ContainsRune(raw, '\\') && utf8.Valid(raw) {
		return raw
	}
	out := []byte{'"'}
	for i := 1; i < len(raw)-1; {
		if raw[i] != '\\' {
			value, size := utf8.DecodeRune(raw[i:])
			out = utf8.AppendRune(out, value)
			i += size
			continue
		}
		if raw[i+1] != 'u' {
			if raw[i+1] == '/' {
				out = append(out, '/')
			} else {
				out = append(out, raw[i:i+2]...)
			}
			i += 2
			continue
		}
		value, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		i += 6
		r := rune(value)
		if utf16.IsSurrogate(r) {
			if r >= 0xd800 && r <= 0xdbff && i+6 < len(raw) && raw[i] == '\\' && raw[i+1] == 'u' {
				low, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if low >= 0xdc00 && low <= 0xdfff {
					out = utf8.AppendRune(out, utf16.DecodeRune(r, rune(low)))
					i += 6
					continue
				}
			}
			out = append(out, strings.ToLower(string(raw[i-6:i]))...)
			continue
		}
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\b':
			out = append(out, '\\', 'b')
		case '\f':
			out = append(out, '\\', 'f')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if r < 0x20 {
				out = append(out, strings.ToLower(string(raw[i-6:i]))...)
			} else {
				out = utf8.AppendRune(out, r)
			}
		}
	}
	return append(out, '"')
}
