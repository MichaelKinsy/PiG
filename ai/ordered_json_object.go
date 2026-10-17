package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// jsonMember is one member of a JSON object: the decoded key and the raw JSON text of its value.
type jsonMember struct {
	key   string
	value json.RawMessage
}

// readJSONObjectMembers returns the members of a JSON object in the order the text has them; as with JavaScript's JSON.parse, a repeated key keeps its first
// position with its last value. A Go map has no order, so a request body that has to leave exactly as Pi writes it is edited member by member.
func readJSONObjectMembers(data []byte) ([]jsonMember, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("not a JSON object")
	}
	var members []jsonMember
	index := map[string]int{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if at, seen := index[key]; seen {
			members[at].value = value
			continue
		}
		index[key] = len(members)
		members = append(members, jsonMember{key: key, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the JSON object")
	}
	return members, nil
}

// jsonSpreadMembers returns the members `{ ...value }` copies from the JSON value data: an object's members (readJSONObjectMembers), an array's elements and a
// string's UTF-16 code units under their index keys, and none for null, a boolean or a number.
func jsonSpreadMembers(data []byte) ([]jsonMember, error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, errors.New("empty JSON value")
	}
	switch trimmed[0] {
	case '{':
		return readJSONObjectMembers(data)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		members := make([]jsonMember, len(items))
		for i, item := range items {
			members[i] = jsonMember{key: strconv.Itoa(i), value: item}
		}
		return members, nil
	case '"':
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, err
		}
		units := utf16.Encode([]rune(text))
		members := make([]jsonMember, len(units))
		for i, unit := range units {
			// JSON.stringify writes a lone surrogate as an escape.
			value := fmt.Appendf(nil, `"\u%04x"`, unit)
			if !utf16.IsSurrogate(rune(unit)) {
				encoded, err := marshalRequestBody(string(rune(unit)))
				if err != nil {
					return nil, err
				}
				value = encoded
			}
			members[i] = jsonMember{key: strconv.Itoa(i), value: value}
		}
		return members, nil
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid JSON value")
	}
	return nil, nil
}

// payloadReplacement is the value a provider sends for what an onPayload hook returned. Pi's hook returns a JavaScript value and the provider sends JSON.stringify
// of it, so JSON text (an extension's replacement) is rewritten as JSON.stringify writes what JSON.parse reads from it: string escapes, shortest numbers, a
// repeated key in its first place with its last value, array-index keys first. Text that is not JSON is left for the encoder to reject.
func payloadReplacement(next any) any {
	if raw, ok := next.(json.RawMessage); ok {
		if canonical, err := jsonstringify.Canonicalize(raw); err == nil {
			return json.RawMessage(canonical)
		}
	}
	return next
}

// writeJSONObjectMembers writes members as one compact JSON object the way JSON.stringify writes an object built from them: array-index keys first in ascending
// order, then the other keys in the order of members, which it sorts in place. Nested values are written as JSON.stringify writes what JSON.parse read from
// their text: members in the order the text has them (a repeated key keeps its first place with its last value, array-index keys first) and Pi's string escapes.
func writeJSONObjectMembers(members []jsonMember) ([]byte, error) {
	jsstring.SortOwnKeys(members, func(member jsonMember) string { return member.key })
	out := []byte{'{'}
	for i, member := range members {
		if i > 0 {
			out = append(out, ',')
		}
		key, err := marshalRequestBody(member.key)
		if err != nil {
			return nil, err
		}
		out = append(out, key...)
		out = append(out, ':')
		if out, err = jsonstringify.AppendCanonical(out, member.value); err != nil {
			return nil, err
		}
	}
	return append(out, '}'), nil
}
