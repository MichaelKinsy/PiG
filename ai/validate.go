// Ports packages/ai/src/utils/validation.ts.
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/typebox"
	"github.com/MichaelKinsy/PiG/third_party/jsonschema"
)

// validateToolArgs clones, normalizes optional nulls, converts, and validates arguments.
// The returned JSON, rather than the model's original input, is passed to hooks and execution. It keeps the member order of args, as Pi validates structuredClone of the arguments and returns it (validation.ts:317-339).
func validateToolArgs(toolName string, schema map[string]any, args json.RawMessage) (json.RawMessage, error) {
	if schema == nil {
		schema = map[string]any{}
	}
	//portlint:allow mapkeyorder a map[string]any schema records no authored member order; ValidateToolArguments passes ToolSchema.ParametersJSON and tools with an authored schema reach ValidateToolArgumentsJSON (agent/tool_execution.go:303)
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return validateToolArgsSchema(toolName, encoded, args)
}

// decodeSchemaInstance decodes a JSON text into the value a schema validator checks. sdkjson keeps a lone surrogate escape as one
// UTF-16 unit, as the structuredClone of JSON.parse's result does; encoding/json would hand the tool U+FFFD.
func decodeSchemaInstance(text json.RawMessage) (any, error) {
	var instance any
	err := sdkjson.Unmarshal(text, &instance)
	return instance, err
}

func validateToolArgsSchema(toolName string, rawSchema, args json.RawMessage) (json.RawMessage, error) {
	entry := toolArgumentSchemas.entry(rawSchema)
	// The parsed schema caches its children and its compiled form as validation reaches them, so one validation at a time uses it.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	schema, err := entry.parsed()
	if err != nil {
		return nil, err
	}
	instance, err := decodeSchemaInstance(args)
	if err != nil {
		return nil, fmt.Errorf("Validation failed for tool %q:\n  - invalid JSON: %w\n\nReceived arguments:\n%s", toolName, err, args)
	}
	// The cached schema outlives this validation; it must not keep the arguments' objects reachable after it.
	defer func() { schema.wrapped, schema.mutated = nil, false }()
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
				return schema.marshalInSourceOrder(coerced, args)
			}
			return schema.marshalInSourceOrder(instance, args)
		}
		instance = coerced
	}
	if err := compiled.Validate(instance); err != nil {
		failures := schema.argumentErrorsAt(schema.sourceOrder(instance, args), instance, "", "")
		// TypeBox's error context keeps the first maxErrors (8) errors and drops the rest.
		failures = failures[:min(len(failures), typeboxMaxErrors)]
		message := strings.Join(failures, "\n")
		if message == "" {
			message = "Unknown validation error"
		}
		return nil, fmt.Errorf("Validation failed for tool \"%s\":\n%s\n\nReceived arguments:\n%s", toolName, message, prettyArguments(args))
	}
	// Nothing wrote to the decoded arguments, so they are JSON.parse(args); when args is already what JSON.stringify writes for that value, re-encoding it in its own member order returns args.
	if !schema.mutated && len(schema.wrapped) == 0 && jsonstringify.IsCanonical(args) {
		return args, nil
	}
	return schema.marshalInSourceOrder(instance, args)
}

// typeboxMaxErrors is typebox 1.3.27 Settings.maxErrors.
// upstream: node_modules/typebox/build/system/settings/settings.mjs:maxErrors
const typeboxMaxErrors = 8

// toolArgumentSchemas holds the parsed and compiled schema of each tool whose arguments were validated. A tool call validates against its tool's schema every time, so parsing and compiling it once per distinct schema leaves only the arguments to decode per call.
var toolArgumentSchemas argumentSchemaCache

const (
	// argumentSchemaCacheEntries bounds how many distinct tool schemas stay parsed and compiled; the oldest is dropped first. A session carries a few dozen tools, so 256 holds every tool of a session with room for extension churn; a dropped schema is parsed again on its next call.
	// pig additive (D112): Pi's validator cache is a WeakMap with no bound (validation.ts:6); the bound keeps a long-lived process from retaining a schema per tool definition it ever saw.
	argumentSchemaCacheEntries = 256
	// argumentSchemaCacheMaxBytes is the size of schema text above which a schema is validated without being kept: 1 MiB is far above any tool schema, and one oversized schema must not displace the small ones.
	// pig additive (D112): see argumentSchemaCacheEntries.
	argumentSchemaCacheMaxBytes = 1 << 20
)

type argumentSchemaCache struct {
	mu      sync.Mutex
	entries map[string]*argumentSchemaEntry
	order   []string
}

type argumentSchemaEntry struct {
	mu     sync.Mutex
	raw    json.RawMessage
	schema *argumentSchema
	err    error
	done   bool
}

// entry returns the cache entry for the schema text. A schema too large to cache gets an entry of its own that is not kept.
func (cache *argumentSchemaCache) entry(raw json.RawMessage) *argumentSchemaEntry {
	if len(raw) > argumentSchemaCacheMaxBytes {
		return &argumentSchemaEntry{raw: raw}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if found := cache.entries[string(raw)]; found != nil {
		return found
	}
	if cache.entries == nil {
		cache.entries = map[string]*argumentSchemaEntry{}
	}
	if len(cache.order) >= argumentSchemaCacheEntries {
		delete(cache.entries, cache.order[0])
		cache.order = cache.order[1:]
	}
	key := string(raw)
	created := &argumentSchemaEntry{raw: json.RawMessage(key)}
	cache.entries[key] = created
	cache.order = append(cache.order, key)
	return created
}

// parsed is parseArgumentSchema of the entry's schema text, computed once. The caller holds entry.mu.
func (entry *argumentSchemaEntry) parsed() (*argumentSchema, error) {
	if !entry.done {
		entry.schema, entry.err = parseArgumentSchema(entry.raw)
		entry.done = true
	}
	return entry.schema, entry.err
}

type argumentSchema struct {
	doc        any
	fields     map[string]json.RawMessage
	keys       []string
	children   map[string]*argumentSchema
	compiled   *jsonschema.Schema
	compileErr error
	root       *argumentSchema
	// matchers holds typeboxPropertyNameMatcher of each property name of a "properties" node, compiled when validation first reaches it. The caller holds entry.mu, as for children.
	matchers map[string]func(string) bool
	// wrapped lists the objects Convert wrapped in arrays during the validation in progress, and is empty between validations; only the root carries it, and the caller holds entry.mu.
	wrapped []wrappedObject
	// mutated records that the validation in progress changed a member or item of the arguments (setMember, setItem, a deleted optional null); only the root carries it, and the caller holds entry.mu.
	mutated bool
}

// setMember writes a converted or coerced member back, noting on the root when it is not the value already there.
func (s *argumentSchema) setMember(object map[string]any, key string, value any) {
	if current, ok := object[key]; !ok || !sameArgumentValue(current, value) {
		s.root.mutated = true
	}
	object[key] = value
}

// setItem is setMember for an array item.
func (s *argumentSchema) setItem(items []any, i int, value any) {
	if !sameArgumentValue(items[i], value) {
		s.root.mutated = true
	}
	items[i] = value
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
	typebox.RegisterFormats(compiler)
	if err := compiler.AddResource("tool.json", typebox.CompilerSchema(s.doc)); err != nil {
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
				s.root.mutated = true
				continue
			}
		}
		child.normalizeOptionalNulls(current)
	}
}

func (s *argumentSchema) eachItem(items []any, visit func(*argumentSchema, any) any) {
	if tuple := s.list("items"); tuple != nil {
		for i := range min(len(items), len(tuple)) {
			s.setItem(items, i, visit(tuple[i], items[i]))
		}
	} else if item := s.child("items"); item != nil {
		if _, ok := item.doc.(map[string]any); ok {
			for i := range items {
				s.setItem(items, i, visit(item, items[i]))
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

// typeboxPropertyNameMatcher is Guard.EntriesRegExp's new RegExp(`^${name}$`), unwrapped as TypeBox writes it (so "a|b" is ^a or b$). A name Go cannot compile matches only
// itself; TypeBox throws there.
func typeboxPropertyNameMatcher(name string) func(string) bool {
	expression, err := regexp.Compile("^" + name + "$")
	if err != nil {
		return func(key string) bool { return key == name }
	}
	return expression.MatchString
}

// nameMatcher is typeboxPropertyNameMatcher(name), compiled once per schema instead of once per validated object.
func (s *argumentSchema) nameMatcher(name string) func(string) bool {
	if matcher, ok := s.matchers[name]; ok {
		return matcher
	}
	if s.matchers == nil {
		s.matchers = map[string]func(string) bool{}
	}
	matcher := typeboxPropertyNameMatcher(name)
	s.matchers[name] = matcher
	return matcher
}

func (s *argumentSchema) eachProperty(value any, native bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	properties := s.child("properties")
	defined := map[string]bool{}
	if properties != nil && native {
		// TypeBox FromObject (value/convert/from_object.mjs, Guard.EntriesRegExp) tests each property name as new RegExp(`^${name}$`) against every key, so a name with
		// regular-expression characters converts the keys it matches and not necessarily itself.
		keys := slices.Sorted(maps.Keys(object))
		for _, name := range properties.keys {
			defined[name] = true
			child, matches := properties.child(name), properties.nameMatcher(name)
			for _, key := range keys {
				if matches(key) {
					s.setMember(object, key, child.convert(object[key]))
				}
			}
		}
	} else if properties != nil {
		for _, key := range properties.keys {
			defined[key] = true
			current, exists := object[key]
			if !exists {
				continue
			}
			child := properties.child(key)
			if native {
				s.setMember(object, key, child.convert(current))
			} else {
				s.setMember(object, key, child.coerce(current))
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
							if !properties.nameMatcher(known)(key) {
								s.setMember(object, key, additional.convert(object[key]))
							}
						}
					}
				} else if !defined[key] {
					s.setMember(object, key, additional.coerce(object[key]))
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
