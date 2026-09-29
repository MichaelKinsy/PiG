package ai

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// schemaObjectOrder retains object-key enumeration across Go map normalization. JSON Schema's required array is emitted in properties enumeration order by Pi.
type schemaObjectOrder map[string][]string

func schemaPath(path, key string) string {
	return path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func readSchemaObjectOrder(data []byte) (schemaObjectOrder, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	order := make(schemaObjectOrder)
	var read func(string) error
	read = func(path string) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			var keys []string
			seen := make(map[string]bool)
			for decoder.More() {
				name, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := name.(string)
				if !ok {
					return fmt.Errorf("schema object key is not a string")
				}
				if !seen[key] {
					keys = append(keys, key)
					seen[key] = true
				}
				if err = read(schemaPath(path, key)); err != nil {
					return err
				}
			}
			keys = javascriptObjectKeyOrder(keys)
			canonical := slices.Clone(keys)
			slices.Sort(canonical)
			canonical = javascriptObjectKeyOrder(canonical)
			if !slices.Equal(keys, canonical) {
				order[path] = keys
			}
		case '[':
			for i := 0; decoder.More(); i++ {
				if err = read(schemaPath(path, strconv.Itoa(i))); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected schema delimiter %q", delimiter)
		}
		_, err = decoder.Token()
		return err
	}
	if err := read(""); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, nil
	}
	return order, nil
}

func javascriptObjectKeyOrder(keys []string) []string {
	type indexKey struct {
		key   string
		index uint64
	}
	var indices []indexKey
	var names []string
	for _, key := range keys {
		index, err := strconv.ParseUint(key, 10, 32)
		if err == nil && index < 1<<32-1 && strconv.FormatUint(index, 10) == key {
			indices = append(indices, indexKey{key, index})
		} else {
			names = append(names, key)
		}
	}
	slices.SortFunc(indices, func(a, b indexKey) int { return cmp.Compare(a.index, b.index) })
	result := make([]string, 0, len(keys))
	for _, key := range indices {
		result = append(result, key.key)
	}
	return append(result, names...)
}

func orderedSchemaKeys(value map[string]any, order schemaObjectOrder, path string) []string {
	keys := make([]string, 0, len(value))
	seen := make(map[string]bool, len(value))
	for _, key := range order[path] {
		if _, ok := value[key]; ok && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	var rest []string
	for key := range value {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	slices.Sort(rest)
	return javascriptObjectKeyOrder(append(keys, rest...))
}

func marshalSchemaWithOrder(value any, order schemaObjectOrder, path string) ([]byte, error) {
	switch value := value.(type) {
	case map[string]any:
		if value == nil {
			return []byte("null"), nil
		}
		keys := orderedSchemaKeys(value, order, path)
		var out bytes.Buffer
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			name, _ := json.Marshal(key)
			out.Write(name)
			out.WriteByte(':')
			encoded, err := marshalSchemaWithOrder(value[key], order, schemaPath(path, key))
			if err != nil {
				return nil, err
			}
			out.Write(encoded)
		}
		out.WriteByte('}')
		return out.Bytes(), nil
	case []any:
		if value == nil {
			return []byte("null"), nil
		}
		var out bytes.Buffer
		out.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			encoded, err := marshalSchemaWithOrder(item, order, schemaPath(path, strconv.Itoa(i)))
			if err != nil {
				return nil, err
			}
			out.Write(encoded)
		}
		out.WriteByte(']')
		return out.Bytes(), nil
	default:
		return json.Marshal(value)
	}
}

// UnmarshalJSON keeps schema key order as well as its validated data values.
func (tool *ToolSchema) UnmarshalJSON(data []byte) error {
	type wire ToolSchema
	var decoded wire
	var envelope struct {
		*wire
		ConstrainedSampling json.RawMessage `json:"constrainedSampling"`
	}
	envelope.wire = &decoded
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if value := envelope.ConstrainedSampling; len(value) > 0 {
		if bytes.Equal(bytes.TrimSpace(value), []byte("false")) {
			decoded.ConstrainedSamplingDisabled = true
		} else if err := json.Unmarshal(value, &decoded.ConstrainedSampling); err != nil {
			return err
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if parameters, ok := fields["parameters"]; ok {
		order, err := readSchemaObjectOrder(parameters)
		if err != nil {
			return err
		}
		decoded.parameterOrder = order
	}
	*tool = ToolSchema(decoded)
	return nil
}

// MarshalJSON preserves imported parameter ordering when tool definitions cross a session or SDK boundary.
func (tool ToolSchema) MarshalJSON() ([]byte, error) {
	var sampling json.RawMessage
	switch {
	case tool.ConstrainedSampling != nil:
		encoded, err := json.Marshal(tool.ConstrainedSampling)
		if err != nil {
			return nil, err
		}
		sampling = encoded
	case tool.ConstrainedSamplingDisabled:
		sampling = json.RawMessage("false")
	}
	if tool.parameterOrder == nil {
		return json.Marshal(struct {
			Name                string          `json:"name"`
			Description         string          `json:"description"`
			Parameters          map[string]any  `json:"parameters"`
			PromptGuidelines    []string        `json:"promptGuidelines,omitempty"`
			ConstrainedSampling json.RawMessage `json:"constrainedSampling,omitempty"`
		}{tool.Name, tool.Description, tool.Parameters, tool.PromptGuidelines, sampling})
	}
	// Validate cycles and unsupported values before recursively preserving order.
	if _, err := json.Marshal(tool.Parameters); err != nil {
		return nil, err
	}
	parameters, err := marshalSchemaWithOrder(tool.Parameters, tool.parameterOrder, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Name                string          `json:"name"`
		Description         string          `json:"description"`
		Parameters          json.RawMessage `json:"parameters"`
		PromptGuidelines    []string        `json:"promptGuidelines,omitempty"`
		ConstrainedSampling json.RawMessage `json:"constrainedSampling,omitempty"`
	}{tool.Name, tool.Description, parameters, tool.PromptGuidelines, sampling})
}
