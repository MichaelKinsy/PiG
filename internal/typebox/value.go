package typebox

// Ports typebox 1.3.27 build/value: check (Check), errors (Errors with the en_US locale phrasing), clean (Clean),
// default (Default) and clone (Clone), over JSON Schema data.
//
// A schema is JSON Schema data: map[string]any, []any and JSON scalars. TypeBox routes Clean and Default by the
// non-enumerable "~kind" of a schema it built; a Go schema has no such marker, so the kind is read from the keywords
// TypeBox emits for it (Object: type object with properties; Record: type object with patternProperties; Array: type
// array with one items schema; Tuple: type array with positional items; Union: anyOf; Intersect: allOf; Ref: $ref).
// Values are strict JSON: nil, bool, float64 (other Go number kinds are accepted), string, []any, map[string]any and
// *chordjson.Object.

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/third_party/jsonschema"
)

// ValueError is one error of Errors, as TypeBox reports it: the keyword that failed, the schema path of the failing schema, the instance path of the failing value ("" for the root; member names appended unescaped, as TypeBox does) and the en_US message.
type ValueError struct {
	Keyword      string
	SchemaPath   string
	InstancePath string
	Message      string
	params       errParams
}

// ParamsObject is the error's params object, its members in the order TypeBox writes them (empty for none).
func (e ValueError) ParamsObject() *chordjson.Object {
	list := func(value any) any {
		if names, ok := value.([]string); ok {
			out := make([]any, len(names))
			for index, name := range names {
				out[index] = name
			}
			return out
		}
		return value
	}
	switch e.Keyword {
	case "type", "format", "pattern", "multipleOf", "additionalProperties", "propertyNames", "unevaluatedItems", "unevaluatedProperties":
		return chordjson.ObjectOf(e.Keyword, list(e.params.val1))
	case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
		return chordjson.ObjectOf("limit", e.params.val1)
	case "const":
		return chordjson.ObjectOf("allowedValue", e.params.val1)
	case "enum":
		return chordjson.ObjectOf("allowedValues", e.params.val1)
	case "required":
		return chordjson.ObjectOf("requiredProperties", list(e.params.val1))
	case "uniqueItems":
		return chordjson.ObjectOf("duplicateItems", e.params.val1)
	case "oneOf":
		return chordjson.ObjectOf("passingSchemas", e.params.val1)
	case "if":
		return chordjson.ObjectOf("failingKeyword", e.params.val1)
	case "exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum":
		return chordjson.ObjectOf("comparison", e.params.val1, "limit", e.params.val2)
	case "dependencies", "dependentRequired":
		return chordjson.ObjectOf("property", e.params.val1, "dependencies", e.params.val2)
	case "contains":
		if e.params.val2 != nil {
			return chordjson.ObjectOf("minContains", e.params.val1, "maxContains", e.params.val2)
		}
		return chordjson.ObjectOf("minContains", e.params.val1)
	}
	return chordjson.NewObject(0)
}

// compiledCacheLimit bounds the compiled-schema cache; a full cache starts over.
const compiledCacheLimit = 512

// compiled caches compiled schemas by their JSON text. compile makes the map on first use, so package initialization allocates nothing for it.
var compiled struct {
	sync.Mutex
	byText map[string]*compiledSchema
}

type compiledSchema struct {
	schema *jsonschema.Schema
	err    error
}

func compile(schema any) (*jsonschema.Schema, error) {
	if schema == nil {
		schema = map[string]any{}
	}
	text, err := jsstring.MarshalJSON(Plain(schema))
	if err != nil {
		return nil, err
	}
	key := string(text)
	compiled.Lock()
	defer compiled.Unlock()
	if entry, ok := compiled.byText[key]; ok {
		return entry.schema, entry.err
	}
	var document any
	//portlint:allow jsonclone chord schema.ts:39 Value.Check(schema, value) compiles TypeBox's schema; the text is the cache key anyway, the decode runs only on a cache miss (once per distinct schema), and it gives the cached validator strict JSON data (json.Number, RawMessage and struct members decoded) that no caller can mutate later
	if err := json.Unmarshal(text, &document); err != nil { //perf:ok the compile cache fill: once per distinct schema text, which is also the cache key
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	RegisterFormats(compiler)
	entry := &compiledSchema{}
	if entry.err = compiler.AddResource("schema.json", CompilerSchema(document)); entry.err == nil {
		entry.schema, entry.err = compiler.Compile("schema.json")
	}
	if compiled.byText == nil {
		compiled.byText = map[string]*compiledSchema{}
	} else if len(compiled.byText) >= compiledCacheLimit {
		clear(compiled.byText)
	}
	compiled.byText[key] = entry
	return entry.schema, entry.err
}

// Check is Value.Check: whether value matches schema. A schema the validator cannot compile matches nothing.
func Check(schema, value any) bool {
	validator, err := compile(schema)
	return err == nil && validator.Validate(Plain(value)) == nil
}

// Plain returns value with every *chordjson.Object as a map[string]any and every Go number as float64, the form the
// validator reads. Other values are returned as they are.
func Plain(value any) any {
	switch typed := value.(type) {
	case *Ordered:
		if typed == nil {
			return nil
		}
		out := make(map[string]any, len(typed.members))
		for key, item := range typed.members {
			out[key] = Plain(item)
		}
		return out
	case *chordjson.Object:
		if typed == nil {
			return nil
		}
		out := make(map[string]any, typed.Len())
		for key, item := range typed.All() {
			out[key] = Plain(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = Plain(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = Plain(item)
		}
		return out
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case float32:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint64:
		return float64(typed)
	case uint32:
		return float64(typed)
	default:
		return value
	}
}

// Clone is Value.Clone: a deep copy of a JSON value; objects keep their kind (map or ordered object).
func Clone(value any) any {
	switch typed := value.(type) {
	case *chordjson.Object:
		if typed == nil {
			return typed
		}
		out := chordjson.NewObject(typed.Len())
		for key, item := range typed.All() {
			out.Set(key, Clone(item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = Clone(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = Clone(item)
		}
		return out
	default:
		return value
	}
}

// object is a mutable view of a JSON object value of either Go kind.
type object struct {
	plain   map[string]any
	ordered *chordjson.Object
}

func asObject(value any) (object, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return object{plain: typed}, true
	case *chordjson.Object:
		return object{ordered: typed}, typed != nil
	}
	return object{}, false
}

func (o object) keys() []string {
	if o.ordered != nil {
		return o.ordered.Keys()
	}
	keys := make([]string, 0, len(o.plain))
	for key := range o.plain {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (o object) get(key string) (any, bool) {
	if o.ordered != nil {
		return o.ordered.Get(key)
	}
	value, ok := o.plain[key]
	return value, ok
}

func (o object) set(key string, value any) {
	if o.ordered != nil {
		o.ordered.Set(key, value)
		return
	}
	o.plain[key] = value
}

func (o object) remove(key string) {
	if o.ordered != nil {
		o.ordered.Delete(key)
		return
	}
	delete(o.plain, key)
}

func schemaMap(schema any) map[string]any {
	typed, _ := objectMap(schema)
	return typed
}

// objectMap reads a schema node held as a Go map or as an ordered *chordjson.Object (members kept as they are: a nested
// node of either kind stays one). An ordered node is copied to a map; callers that need its member order read it from the
// *chordjson.Object (properties).
func objectMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case *Ordered:
		if typed == nil {
			return nil, false
		}
		return typed.members, true
	case *chordjson.Object:
		if typed == nil {
			return nil, false
		}
		out := maps.Collect(typed.All())
		return out, true
	}
	return nil, false
}

func typeIs(schema map[string]any, name string) bool { return schema["type"] == name }

func isObjectKind(schema map[string]any) bool {
	_, ok := objectMap(schema["properties"])
	return typeIs(schema, "object") && ok
}

func isRecordKind(schema map[string]any) bool {
	patterns, ok := objectMap(schema["patternProperties"])
	return typeIs(schema, "object") && ok && len(patterns) == 1 && !isObjectKind(schema)
}

func isTupleKind(schema map[string]any) bool {
	_, ok := schema["items"].([]any)
	return typeIs(schema, "array") && ok
}

func isArrayKind(schema map[string]any) bool {
	_, ok := objectMap(schema["items"])
	return typeIs(schema, "array") && ok
}

func recordEntry(schema map[string]any) (*regexp.Regexp, any) {
	patterns, _ := objectMap(schema["patternProperties"])
	for pattern, value := range patterns {
		expression, err := CompilePattern(pattern)
		if err != nil {
			return nil, value
		}
		return expression, value
	}
	return nil, nil
}

func properties(schema map[string]any) (map[string]any, []string) {
	if ordered, ok := schema["properties"].(*Ordered); ok && ordered != nil {
		return ordered.members, ordered.keys
	}
	if ordered, ok := schema["properties"].(*chordjson.Object); ok && ordered != nil {
		// Members are visited in the order the schema declared them, as TypeBox's Object.keys does.
		props, _ := objectMap(ordered)
		return props, ordered.Keys()
	}
	props, _ := schema["properties"].(map[string]any)
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return props, keys
}

// Clean is Value.Clean (value/clean): it removes the properties and tuple elements schema does not evaluate. It
// mutates value and returns the result.
func Clean(schema, value any) any {
	s := schemaMap(schema)
	switch {
	case s == nil:
		return value
	case isArrayKind(s):
		items, ok := value.([]any)
		if !ok {
			return value
		}
		for index, item := range items {
			items[index] = Clean(s["items"], item)
		}
		return items
	case isObjectKind(s):
		target, ok := asObject(value)
		if !ok {
			return value
		}
		props, _ := properties(s)
		additional, hasAdditional := s["additionalProperties"]
		for _, key := range target.keys() {
			item, _ := target.get(key)
			if property, known := props[key]; known {
				target.set(key, Clean(property, item))
				continue
			}
			if hasAdditional && (additional == true || (schemaMap(additional) != nil && Check(additional, item))) {
				target.set(key, Clean(additional, item))
				continue
			}
			target.remove(key)
		}
		return value
	case isRecordKind(s):
		target, ok := asObject(value)
		if !ok {
			return value
		}
		pattern, recordValue := recordEntry(s)
		additional, hasAdditional := s["additionalProperties"]
		for _, key := range target.keys() {
			item, _ := target.get(key)
			if pattern != nil && pattern.MatchString(key) {
				target.set(key, Clean(recordValue, item))
				continue
			}
			if hasAdditional && (additional == true || (schemaMap(additional) != nil && Check(additional, item))) {
				target.set(key, Clean(additional, item))
				continue
			}
			target.remove(key)
		}
		return value
	case isTupleKind(s):
		items, ok := value.([]any)
		if !ok {
			return value
		}
		positional := s["items"].([]any)
		length := min(len(items), len(positional))
		for index := range length {
			items[index] = Clean(positional[index], items[index])
		}
		return items[:length]
	case s["anyOf"] != nil:
		branches, _ := s["anyOf"].([]any)
		for _, branch := range branches {
			cleaned := Clean(branch, Clone(value))
			if Check(branch, cleaned) {
				return cleaned
			}
		}
		return value
	case s["allOf"] != nil:
		if merged := evaluateIntersect(s); merged != nil {
			return Clean(merged, value)
		}
		return value
	}
	return value
}

// evaluateIntersect is TypeBox's Evaluate of an intersection of objects: one object with every member's properties
// and required names; unevaluatedProperties becomes additionalProperties. It returns nil for other intersections.
func evaluateIntersect(schema map[string]any) map[string]any {
	members, _ := schema["allOf"].([]any)
	props := map[string]any{}
	var required []any
	for _, member := range members {
		m := schemaMap(member)
		if m == nil || !isObjectKind(m) {
			return nil
		}
		inner, _ := objectMap(m["properties"])
		maps.Copy(props, inner)
		if names, ok := m["required"].([]any); ok {
			required = append(required, names...)
		}
	}
	merged := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		merged["required"] = required
	}
	if additional, ok := schema["unevaluatedProperties"]; ok {
		merged["additionalProperties"] = additional
	}
	return merged
}

// undefined stands for a JavaScript undefined while Default walks: an absent property or element.
type undefined struct{}

// Default is Value.Default (value/default): it fills absent values that have a schema default. It mutates value and
// returns the result; an absent root is passed as nil and returned as nil when schema gives it no default.
func Default(schema, value any) any {
	result := defaultValue(schema, value, false)
	if _, absent := result.(undefined); absent {
		return nil
	}
	return result
}

func defaultValue(schema, value any, absent bool) any {
	s := schemaMap(schema)
	if s == nil {
		if absent {
			return undefined{}
		}
		return value
	}
	current := value
	if absent {
		current = undefined{}
		if fallback, ok := s["default"]; ok {
			current = Plain(fallback)
		}
	}
	if _, missing := current.(undefined); missing {
		return current
	}
	switch {
	case isArrayKind(s):
		items, ok := current.([]any)
		if !ok {
			return current
		}
		for index, item := range items {
			items[index] = defaultValue(s["items"], item, false)
		}
		return items
	case isObjectKind(s):
		target, ok := asObject(current)
		if !ok {
			return current
		}
		props, keys := properties(s)
		for _, key := range keys {
			property := props[key]
			item, present := target.get(key)
			result := defaultValue(property, item, !present)
			if _, missing := result.(undefined); missing {
				continue
			}
			target.set(key, result)
		}
		if additional := schemaMap(s["additionalProperties"]); additional != nil {
			for _, key := range target.keys() {
				if _, known := props[key]; known {
					continue
				}
				item, _ := target.get(key)
				target.set(key, defaultValue(additional, item, false))
			}
		}
		return current
	case isRecordKind(s):
		target, ok := asObject(current)
		if !ok {
			return current
		}
		pattern, recordValue := recordEntry(s)
		if _, hasDefault := schemaMap(recordValue)["default"]; hasDefault && pattern != nil {
			for _, key := range target.keys() {
				if pattern.MatchString(key) {
					item, _ := target.get(key)
					target.set(key, defaultValue(recordValue, item, false))
				}
			}
		}
		if additional := schemaMap(s["additionalProperties"]); additional != nil {
			for _, key := range target.keys() {
				if pattern != nil && pattern.MatchString(key) {
					continue
				}
				item, _ := target.get(key)
				target.set(key, defaultValue(additional, item, false))
			}
		}
		return current
	case isTupleKind(s):
		items, ok := current.([]any)
		if !ok {
			return current
		}
		// default/from_tuple.mjs assigns every positional index, so a short value grows to the tuple's length; an
		// element without a default is undefined, which JSON writes as null.
		positional := s["items"].([]any)
		for index := range positional {
			if index < len(items) {
				items[index] = defaultValue(positional[index], items[index], false)
				continue
			}
			result := defaultValue(positional[index], nil, true)
			if _, missing := result.(undefined); missing {
				result = nil
			}
			items = append(items, result)
		}
		return items
	case s["anyOf"] != nil:
		branches, _ := s["anyOf"].([]any)
		for _, branch := range branches {
			result := defaultValue(branch, Clone(current), false)
			if Check(branch, result) {
				return result
			}
		}
		return current
	case s["allOf"] != nil:
		if merged := evaluateIntersect(s); merged != nil {
			return defaultValue(merged, current, false)
		}
	}
	return current
}
