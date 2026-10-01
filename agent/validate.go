// Ports packages/ai/src/utils/validation.ts.
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/MichaelKinsy/PiG/ai"
)

// validateToolArgs clones, normalizes optional nulls, converts, and validates arguments.
// The returned JSON, rather than the model's original input, is passed to hooks and execution. It keeps the member order of args, as Pi validates structuredClone of the arguments and returns it (validation.ts:317-339).
func validateToolArgs(toolName string, schema map[string]any, args json.RawMessage) (json.RawMessage, error) {
	if schema == nil {
		schema = map[string]any{}
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return validateToolArgsSchema(toolName, encoded, args)
}

func validateToolArgsSchema(toolName string, rawSchema, args json.RawMessage) (json.RawMessage, error) {
	schema, err := parseArgumentSchema(rawSchema)
	if err != nil {
		return nil, err
	}
	var instance any
	if err := json.Unmarshal(args, &instance); err != nil {
		return nil, fmt.Errorf("Validation failed for tool %q:\n  - invalid JSON: %w\n\nReceived arguments:\n%s", toolName, err, args)
	}
	schema.normalizeOptionalNulls(instance)
	// Pi ignores Convert's return value. Interior mutations survive; replacing the root does not.
	schema.convert(instance)
	compiled, err := schema.validator()
	if err != nil {
		return nil, err
	}
	coerced := instance
	if schema.value("~skipCoercion") != true {
		coerced = schema.coerce(instance)
	}
	if !sameArgumentValue(coerced, instance) {
		_, fromObject := instance.(map[string]any)
		_, toObject := coerced.(map[string]any)
		if !fromObject || !toObject {
			if compiled.Validate(coerced) == nil {
				return ai.MarshalJSONInSourceOrder(coerced, args)
			}
			return ai.MarshalJSONInSourceOrder(instance, args)
		}
		instance = coerced
	}
	if err := compiled.Validate(instance); err != nil {
		failures := schema.argumentValidationErrors(instance, "")
		message := strings.Join(failures, "\n")
		if message == "" {
			message = "Unknown validation error"
		}
		return nil, fmt.Errorf("Validation failed for tool \"%s\":\n%s\n\nReceived arguments:\n%s", toolName, message, prettyArguments(args))
	}
	return ai.MarshalJSONInSourceOrder(instance, args)
}

type argumentSchema struct {
	doc        any
	fields     map[string]json.RawMessage
	keys       []string
	children   map[string]*argumentSchema
	compiled   *jsonschema.Schema
	compileErr error
	root       *argumentSchema
}

func parseArgumentSchema(raw json.RawMessage) (*argumentSchema, error) {
	s := &argumentSchema{children: map[string]*argumentSchema{}}
	s.root = s
	if err := json.Unmarshal(raw, &s.doc); err != nil {
		return nil, err
	}
	if _, ok := s.doc.(map[string]any); !ok {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s.fields); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		s.keys = append(s.keys, key.(string))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
	}
	s.keys = argumentObjectKeys(s.keys)
	return s, nil
}

func (s *argumentSchema) child(key string) *argumentSchema {
	if child, ok := s.children[key]; ok {
		return child
	}
	raw, ok := s.fields[key]
	if !ok {
		return nil
	}
	child, err := parseArgumentSchema(raw)
	if err != nil {
		return nil
	}
	child.root = s.root
	s.children[key] = child
	return child
}

func (s *argumentSchema) list(key string) []*argumentSchema {
	var raws []json.RawMessage
	if json.Unmarshal(s.fields[key], &raws) != nil {
		return nil
	}
	out := make([]*argumentSchema, 0, len(raws))
	for i, raw := range raws {
		cacheKey := fmt.Sprintf("%s/%d", key, i)
		child, ok := s.children[cacheKey]
		if !ok {
			child, _ = parseArgumentSchema(raw)
			child.root = s.root
			s.children[cacheKey] = child
		}
		out = append(out, child)
	}
	return out
}

func (s *argumentSchema) value(key string) any {
	object, _ := s.doc.(map[string]any)
	return object[key]
}

func (s *argumentSchema) text(key string) string { value, _ := s.value(key).(string); return value }

func (s *argumentSchema) types() []string {
	if typ := s.text("type"); typ != "" {
		return []string{typ}
	}
	var types []string
	for _, value := range anySlice(s.value("type")) {
		if typ, ok := value.(string); ok {
			types = append(types, typ)
		}
	}
	return types
}

func anySlice(value any) []any { values, _ := value.([]any); return values }

func (s *argumentSchema) validator() (*jsonschema.Schema, error) {
	if s.compiled != nil || s.compileErr != nil {
		return s.compiled, s.compileErr
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("tool.json", argumentCompilerSchema(s.doc)); err != nil {
		s.compileErr = err
		return nil, err
	}
	s.compiled, s.compileErr = compiler.Compile("tool.json")
	return s.compiled, s.compileErr
}

func (s *argumentSchema) check(value any) bool {
	validator, err := s.validator()
	return err == nil && validator.Validate(value) == nil
}

func (s *argumentSchema) normalizeOptionalNulls(value any) {
	if items, ok := value.([]any); ok {
		s.eachItem(items, func(child *argumentSchema, item any) any { child.normalizeOptionalNulls(item); return item })
		return
	}
	object, ok := value.(map[string]any)
	properties := s.child("properties")
	if !ok || properties == nil {
		return
	}
	required := anySlice(s.value("required"))
	for _, key := range properties.keys {
		current, exists := object[key]
		if !exists {
			continue
		}
		child := properties.child(key)
		if current == nil && !slices.Contains(required, any(key)) && child.text("$ref") == "" {
			if validator, err := child.validator(); err == nil && validator.Validate(nil) != nil {
				delete(object, key)
				continue
			}
		}
		child.normalizeOptionalNulls(current)
	}
}

func (s *argumentSchema) eachItem(items []any, visit func(*argumentSchema, any) any) {
	if tuple := s.list("items"); tuple != nil {
		for i := range min(len(items), len(tuple)) {
			items[i] = visit(tuple[i], items[i])
		}
	} else if item := s.child("items"); item != nil {
		if _, ok := item.doc.(map[string]any); ok {
			for i := range items {
				items[i] = visit(item, items[i])
			}
		}
	}
}

func (s *argumentSchema) coerce(value any) any {
	for _, child := range s.list("allOf") {
		value = child.coerce(value)
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if children := s.list(key); children != nil {
			value = coerceArgumentUnion(value, children, false)
		}
	}
	types := s.types()
	matches := len(types) > 1 && slices.ContainsFunc(types, func(typ string) bool { return matchesArgumentType(value, typ) })
	if !matches {
		for _, typ := range types {
			converted := coerceArgumentPrimitive(value, typ, false)
			if !sameArgumentValue(value, converted) {
				value = converted
				break
			}
		}
	}
	if slices.Contains(types, "object") {
		s.eachProperty(value, false)
	}
	if slices.Contains(types, "array") {
		if items, ok := value.([]any); ok {
			s.eachItem(items, func(child *argumentSchema, item any) any { return child.coerce(item) })
		}
	}
	return value
}

func coerceArgumentUnion(value any, children []*argumentSchema, native bool) any {
	for _, child := range children {
		if child.check(value) {
			return value
		}
	}
	for _, child := range children {
		candidate := cloneArgumentValue(value)
		if native {
			candidate = child.convert(candidate)
		} else {
			candidate = child.coerce(candidate)
		}
		if native {
			// TypeBox tests each converted candidate against the complete union, not just its arm.
			if slices.ContainsFunc(children, func(arm *argumentSchema) bool { return arm.check(candidate) }) {
				return candidate
			}
		} else if child.check(candidate) {
			return candidate
		}
	}
	return value
}

func (s *argumentSchema) eachProperty(value any, native bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	properties := s.child("properties")
	defined := map[string]bool{}
	if properties != nil {
		for _, key := range properties.keys {
			defined[key] = true
			current, exists := object[key]
			if !exists {
				continue
			}
			child := properties.child(key)
			if native {
				object[key] = child.convert(current)
			} else {
				object[key] = child.coerce(current)
			}
		}
	}
	if additional := s.child("additionalProperties"); additional != nil {
		if _, ok := additional.doc.(map[string]any); ok {
			for _, key := range slices.Sorted(maps.Keys(object)) {
				if native {
					// FromAdditionalProperties converts once for every nonmatching known property.
					if properties != nil {
						for _, known := range properties.keys {
							if key != known {
								object[key] = additional.convert(object[key])
							}
						}
					}
				} else if !defined[key] {
					object[key] = additional.coerce(object[key])
				}
			}
		}
	}
}

func cloneArgumentValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = cloneArgumentValue(child)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = cloneArgumentValue(child)
		}
		return result
	default:
		return value
	}
}

func sameArgumentValue(left, right any) bool {
	// JavaScript compares containers by identity; coercion mutates them in place.
	switch left := left.(type) {
	case map[string]any:
		_, ok := right.(map[string]any)
		return ok && reflect.ValueOf(left).Pointer() == reflect.ValueOf(right).Pointer()
	case []any:
		rightSlice, ok := right.([]any)
		return ok && len(left) == len(rightSlice) && reflect.ValueOf(left).Pointer() == reflect.ValueOf(right).Pointer()
	default:
		switch right.(type) {
		case map[string]any, []any:
			return false
		}
		return left == right
	}
}

func prettyArguments(raw json.RawMessage) string {
	encoded, err := stringifyArgumentJSON(raw)
	if err != nil {
		return string(raw)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encoded, "", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}
