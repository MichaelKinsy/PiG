package ai

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
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

// writtenSchemaObjectOrder is readSchemaObjectOrder of marshalSchemaWithOrder(value, written, ""), read from the walk that writes the text
// rather than from the text: each object's keys in the order they are written, recorded when that is not byte-sorted. value holds only
// map[string]any, []any and scalars, nested at most a bounded depth (a DecodedFormBlock argument), so its keys are distinct.
func writtenSchemaObjectOrder(value JsonObject, written schemaObjectOrder) schemaObjectOrder {
	order := make(schemaObjectOrder)
	var walk func(value any, path string)
	walk = func(value any, path string) {
		switch value := value.(type) {
		case map[string]any:
			keys := orderedSchemaKeys(value, written, path)
			if enumerated := unsortedKeyOrder(keys); enumerated != nil {
				order[path] = enumerated
			}
			for _, key := range keys {
				if member := value[key]; isSchemaContainer(member) {
					walk(member, schemaPath(path, key))
				}
			}
		case []any:
			for i, item := range value {
				if isSchemaContainer(item) {
					walk(item, schemaPath(path, strconv.Itoa(i)))
				}
			}
		}
	}
	walk(map[string]any(value), "")
	if len(order) == 0 {
		return nil
	}
	return order
}

// unsortedKeyOrder returns the enumeration order of an object's distinct keys when it differs from the byte-sorted order encoding/json writes for an unrecorded object, and nil otherwise. Integer-like keys enumerate first and ascending, so an object with one is recorded unless that order is also byte-sorted.
func unsortedKeyOrder(keys []string) []string {
	keys = javascriptObjectKeyOrder(keys)
	if slices.IsSorted(keys) {
		return nil
	}
	return keys
}

// dropSubtree removes the order recorded at path and below it, and returns what it removed.
func (order schemaObjectOrder) dropSubtree(path string) schemaObjectOrder {
	var removed schemaObjectOrder
	for recorded, keys := range order {
		if recorded == path || strings.HasPrefix(recorded, path+"/") {
			if removed == nil {
				removed = schemaObjectOrder{}
			}
			removed[recorded] = keys
			delete(order, recorded)
		}
	}
	return removed
}

// mayBeArrayIndex reports whether key can be an array index: ASCII digits only. It keeps ParseUint, which allocates an error for every other key, off the common path.
func mayBeArrayIndex(key string) bool {
	if key == "" || len(key) > 10 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	return true
}

func hasArrayIndexKey(keys []string) bool {
	return slices.ContainsFunc(keys, mayBeArrayIndex)
}

func javascriptObjectKeyOrder(keys []string) []string {
	if !hasArrayIndexKey(keys) {
		return keys
	}
	type indexKey struct {
		key   string
		index uint64
	}
	var indices []indexKey
	var names []string
	for _, key := range keys {
		if mayBeArrayIndex(key) {
			if index, err := strconv.ParseUint(key, 10, 32); err == nil && index < 1<<32-1 && strconv.FormatUint(index, 10) == key {
				indices = append(indices, indexKey{key, index})
				continue
			}
		}
		names = append(names, key)
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
	return appendSchemaWithOrder(nil, value, order, path, nil)
}

// schemaContainer identifies a map or slice being walked: encoding/json's own cycle key, the address of the first element or
// bucket and the length.
type schemaContainer struct {
	address uintptr
	length  int
}

// schemaWalk is the state of a checked walk: the containers on the path from the root to the value being walked. A container
// met again on that path is a cycle, which encoding/json reports as an error, so the walk hands it back instead of looping.
type schemaWalk struct {
	path []schemaContainer
}

// enter records a container as being walked and reports false when it is already on the path. identity is the map or a
// pointer to the slice's first element, which are pointer-shaped and so are boxed without allocating. A container with no
// member cannot lead back to itself, so a caller skips an empty one.
func (walk *schemaWalk) enter(identity any, length int) bool {
	container := schemaContainer{address: reflect.ValueOf(identity).Pointer(), length: length}
	if slices.Contains(walk.path, container) {
		return false
	}
	walk.path = append(walk.path, container)
	return true
}

func (walk *schemaWalk) leave() {
	walk.path = walk.path[:len(walk.path)-1]
}

// errSchemaUnchecked stops a checked walk at a cycle, or at a value whose encoding/json validity the walk does not decide.
var errSchemaUnchecked = errors.New("schema needs encoding/json validation")

// appendSchemaWithOrder appends JSON.stringify(value) with object keys in their recorded order. A scalar's path is never
// looked up, so only a container member gets one. A non-nil walk makes it checked: it stops at a cycle and
// admits only values that encoding/json and JSON.stringify encode alike, reporting errSchemaUnchecked otherwise. A nil walk
// walks everything.
func appendSchemaWithOrder(out []byte, value any, order schemaObjectOrder, path string, walk *schemaWalk) ([]byte, error) {
	var err error
	switch value := value.(type) {
	case map[string]any:
		if value == nil {
			return append(out, "null"...), nil
		}
		walked := walk != nil && len(value) > 0
		if walked && !walk.enter(value, len(value)) {
			return nil, errSchemaUnchecked
		}
		out = append(out, '{')
		for i, key := range orderedSchemaKeys(value, order, path) {
			if i > 0 {
				out = append(out, ',')
			}
			out = jsstring.AppendString(out, key)
			out = append(out, ':')
			member, memberPath := value[key], ""
			if isSchemaContainer(member) {
				memberPath = schemaPath(path, key)
			}
			if out, err = appendSchemaWithOrder(out, member, order, memberPath, walk); err != nil {
				return nil, err
			}
		}
		if walked {
			walk.leave()
		}
		return append(out, '}'), nil
	case []any:
		if value == nil {
			return append(out, "null"...), nil
		}
		walked := walk != nil && len(value) > 0
		if walked && !walk.enter(&value[0], len(value)) {
			return nil, errSchemaUnchecked
		}
		out = append(out, '[')
		for i, item := range value {
			if i > 0 {
				out = append(out, ',')
			}
			itemPath := ""
			if isSchemaContainer(item) {
				itemPath = schemaPath(path, strconv.Itoa(i))
			}
			if out, err = appendSchemaWithOrder(out, item, order, itemPath, walk); err != nil {
				return nil, err
			}
		}
		if walked {
			walk.leave()
		}
		return append(out, ']'), nil
	case string:
		return jsstring.AppendString(out, value), nil
	case bool:
		return strconv.AppendBool(out, value), nil
	case nil:
		return append(out, "null"...), nil
	case float64:
		if walk != nil && (math.IsNaN(value) || math.IsInf(value, 0)) {
			// encoding/json rejects a non-finite number that JSON.stringify writes as null.
			return nil, errSchemaUnchecked
		}
	case int, int64:
	default:
		if walk != nil {
			// Only the types above are known to encode alike in encoding/json and JSON.stringify.
			return nil, errSchemaUnchecked
		}
	}
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		return nil, err
	}
	return append(out, encoded...), nil
}

// isSchemaContainer reports whether appendSchemaWithOrder walks value's members, which is when value's path matters.
func isSchemaContainer(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	}
	return false
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
	parameters, err := marshalToolParameters(tool.Parameters, tool.parameterOrder)
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

// ParametersJSON encodes Parameters as JSON.stringify writes the schema: a decoded schema keeps its authored member order, and an object the order does not record (a Go-authored map) has sorted members.
func (tool ToolSchema) ParametersJSON() ([]byte, error) {
	return marshalToolParameters(tool.Parameters, tool.parameterOrder)
}

// marshalToolParameters writes a tool's JSON Schema parameters with each object's members in the order JSON.stringify writes them: the
// authored order of a decoded schema, and sorted order for an object the order does not record (a Go-authored map).
func marshalToolParameters(parameters map[string]any, order schemaObjectOrder) ([]byte, error) {
	if order == nil {
		//portlint:allow mapkeyorder no recorded order: a Go-authored map has no insertion order for JSON.stringify to keep, so its members are sorted (doc above)
		return json.Marshal(parameters)
	}
	var pathStorage [16]schemaContainer
	encoded, err := appendSchemaWithOrder(nil, parameters, order, "", &schemaWalk{path: pathStorage[:0]})
	if err != nil {
		// A cycle or an unsupported value fails as encoding/json reports it; anything else is walked unchecked.
		//portlint:allow mapkeyorder only the encode error is used; the bytes are discarded
		if _, invalid := json.Marshal(parameters); invalid != nil {
			return nil, invalid
		}
		if !errors.Is(err, errSchemaUnchecked) {
			return nil, err
		}
		if encoded, err = marshalSchemaWithOrder(parameters, order, ""); err != nil {
			return nil, err
		}
	}
	return encoded, nil
}
