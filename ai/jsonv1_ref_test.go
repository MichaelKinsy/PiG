package ai

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func orderedJSONObjectV1(data []byte) ([]orderedJSONEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("not a JSON object")
	}
	var entries []orderedJSONEntry
	index := map[string]int{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("invalid JSON object key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if position, seen := index[key]; seen {
			entries[position].value = value
			continue
		}
		index[key] = len(entries)
		entries = append(entries, orderedJSONEntry{key: key, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON object")
	}
	return entries, nil
}

func readJSONObjectMembersV1(data []byte) ([]jsonMember, error) {
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

func (s *OrderedSections) UnmarshalJSONV1(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("system message sections must be an object")
	}
	sections := OrderedSections{}
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := nameToken.(string)
		if !ok {
			return fmt.Errorf("system message section name must be a string")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		var value *string
		if string(raw) != "null" {
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return fmt.Errorf("system message section %q: %w", name, err)
			}
			value = &text
		}
		if index := slices.IndexFunc(sections, func(section PromptSection) bool { return section.Name == name }); index >= 0 {
			sections[index].Value = value
		} else {
			sections = append(sections, PromptSection{Name: name, Value: value})
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	// JSON.parse yields an object whose integer-like keys enumerate first.
	jsstring.SortOwnKeys(sections, func(section PromptSection) string { return section.Name })
	*s = sections
	return nil
}

func decodeOpenAIReasoningDetailV1(raw json.RawMessage) (*openAIReasoningDetail, bool) {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, false
	}
	stringOrNull := func(name string) bool {
		value, ok := values[name]
		if !ok || bytes.Equal(value, []byte("null")) {
			return true
		}
		var decoded string
		return json.Unmarshal(value, &decoded) == nil
	}
	if !stringOrNull("id") {
		return nil, false
	}
	if value, ok := values["format"]; ok {
		if bytes.Equal(value, []byte("null")) {
			return nil, false
		}
		var decoded string
		if json.Unmarshal(value, &decoded) != nil {
			return nil, false
		}
	}
	if value, ok := values["index"]; ok {
		if bytes.Equal(value, []byte("null")) {
			return nil, false
		}
		var decoded float64
		if json.Unmarshal(value, &decoded) != nil {
			return nil, false
		}
	}
	var typ string
	if json.Unmarshal(values["type"], &typ) != nil {
		return nil, false
	}
	required := ""
	switch typ {
	case "reasoning.text":
		if !stringOrNull("signature") {
			return nil, false
		}
		required = "text"
	case "reasoning.summary":
		required = "summary"
	case "reasoning.encrypted":
		required = "data"
	default:
		return nil, false
	}
	var requiredValue string
	requiredRaw, hasRequired := values[required]
	// pig divergence (D122): OpenRouter streams the signature of a reasoning.text block in a delta without text (pi-mono #9534); Pi drops it.
	signatureOnly := typ == "reasoning.text" && !hasRequired && isJSONString(values["signature"])
	if !signatureOnly && (bytes.Equal(requiredRaw, []byte("null")) || json.Unmarshal(requiredRaw, &requiredValue) != nil) {
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	detail := &openAIReasoningDetail{typ: typ}
	positions := map[string]int{}
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		name, ok := nameToken.(string)
		if !ok {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		if position, exists := positions[name]; exists {
			detail.fields[position].value = value
			continue
		}
		positions[name] = len(detail.fields)
		detail.fields = append(detail.fields, openAIReasoningDetailField{name: name, value: value})
	}
	return detail, true
}

func parseStoredModelsV1(data []byte) ([]storedModels, modelsRoot, error) {
	if len(data) == 0 {
		return nil, modelsRoot{}, nil
	}
	// JSON.parse's SyntaxError is what the caller reports, with V8's message.
	if err := jsonparse.Validate(data); err != nil {
		return nil, modelsRoot{}, err
	}
	switch first := bytes.TrimSpace(data)[0]; first {
	case 'n':
		return nil, modelsRoot{}, errModelsNullRoot
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, modelsRoot{}, fmt.Errorf("models store: parse: %w", err)
		}
		entries := make([]storedModels, 0, len(items))
		for i, item := range items {
			entries = append(entries, storedModels{providerID: strconv.Itoa(i), raw: item})
		}
		return entries, modelsRoot{kind: modelsRootArray, arrayLen: len(items)}, nil
	case 't', 'f':
		return []storedModels{}, modelsRoot{kind: modelsRootScalar, scalar: string(bytes.TrimSpace(data)), scalarType: "boolean"}, nil
	case '{':
	default:
		if first != '"' {
			canonical, err := jsonstringify.Canonicalize(data)
			if err != nil {
				return nil, modelsRoot{}, err
			}
			return []storedModels{}, modelsRoot{kind: modelsRootScalar, scalar: string(canonical), scalarType: "number"}, nil
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, modelsRoot{}, fmt.Errorf("models store: parse: expected a JSON object")
	}
	var entries []storedModels
	positions := make(map[string]int)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, modelsRoot{}, fmt.Errorf("models store: parse: %w", err)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, modelsRoot{}, fmt.Errorf("models store: parse: %w", err)
		}
		providerID := key.(string)
		if index, exists := positions[providerID]; exists {
			entries[index].raw = raw
		} else {
			positions[providerID] = len(entries)
			entries = append(entries, storedModels{providerID: providerID, raw: raw})
		}
	}
	return entries, modelsRoot{}, nil
}

func orderedCatalogPairsV1(data []byte) ([]catalogPair, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("not a JSON object")
	}
	var pairs []catalogPair
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		pairs = append(pairs, catalogPair{key: keyToken.(string), value: bytes.Clone(value)})
	}
	return pairs, nil
}

func ErrorKeyBeforeTimestampV1(data []byte) bool {
	if !bytes.Contains(data, []byte(`"errorMessage"`)) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		switch key {
		case "errorMessage":
			return true
		case "timestamp":
			return false
		}
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return false
		}
	}
	return false
}

func readSchemaObjectOrderV1(data []byte) (schemaObjectOrder, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	order := make(schemaObjectOrder)
	// read decodes the value at segment below parent (the root has no segment). Only a container needs its path, so a scalar allocates none.
	var read func(parent, segment string, nested bool) error
	read = func(parent, segment string, nested bool) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		path := parent
		if nested {
			path = schemaPath(parent, segment)
		}
		switch delimiter {
		case '{':
			var keys []string
			var seen map[string]bool // built only for an object with many keys; a few keys are searched in keys
			for decoder.More() {
				before := decoder.InputOffset()
				name, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := name.(string)
				if !ok {
					return fmt.Errorf("schema object key is not a string")
				}
				if strings.ContainsRune(key, utf8.RuneError) {
					// encoding/json turns an escaped lone surrogate into U+FFFD, but the decoded object keeps it as JSON.parse does, so the order must name the same key.
					literal := data[before:decoder.InputOffset()]
					if quote := bytes.IndexByte(literal, '"'); quote >= 0 {
						var faithful string
						if sdkjson.Unmarshal(literal[quote:], &faithful) == nil {
							key = faithful
						}
					}
				}
				repeated := false
				if seen != nil {
					if repeated = seen[key]; !repeated {
						keys = append(keys, key)
						seen[key] = true
					}
				} else if repeated = slices.Contains(keys, key); !repeated {
					keys = append(keys, key)
					if len(keys) > 16 {
						seen = make(map[string]bool, len(keys))
						for _, existing := range keys {
							seen[existing] = true
						}
					}
				}
				if repeated {
					// JSON.parse keeps a repeated key's first position but its last value, whose own member order replaces the earlier value's.
					order.dropSubtree(schemaPath(path, key))
				}
				if err = read(path, key, true); err != nil {
					return err
				}
			}
			// JSON.parse keeps a duplicate key's first position but its last value, so a later object whose order needs no record clears an earlier one.
			if enumerated := unsortedKeyOrder(keys); enumerated != nil {
				order[path] = enumerated
			} else {
				delete(order, path)
			}
		case '[':
			for i := 0; decoder.More(); i++ {
				if err = read(path, strconv.Itoa(i), true); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected schema delimiter %q", delimiter)
		}
		_, err = decoder.Token()
		return err
	}
	if err := read("", "", false); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, nil
	}
	return order, nil
}
