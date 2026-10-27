package sdk

import (
	"fmt"
	"maps"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// TSchema is a TypeBox schema value, what pi-ai's `Type.X(...)` returns. Its enumerable members are a JSON Schema object (what JSON.stringify and the host see, keys in the order
// TypeBox wrote them) and it carries TypeBox's hidden members (`~kind`, `~optional`, `~readonly`, `~immutable`, `~unsafe`), which JSON drops but which the constructors and the
// guards read: Type.Object builds `required` from the properties that are not `~optional`, and Type.IsString is true for Type.String() and false for the JSON `{"type":"string"}`.
// A TSchema is immutable: every constructor returns a new one.
type TSchema struct {
	object *OrderedObject
	hidden map[string]any
	// items is the list a schema made of a JavaScript array is (Type.Unsafe, Type.Optional and the other updates accept an array, whose own members JSON.stringify ignores): it
	// is nil for an object.
	items []any
}

// MarshalJSON writes what JSON.stringify of the TypeBox value writes: the enumerable members in order, numbers as JavaScript prints them (1e21, 1e-7, 0 for -0), and strings
// with only the escapes JSON.stringify writes.
func (s *TSchema) MarshalJSON() ([]byte, error) {
	if s.items != nil {
		return json.MarshalStringify(s.items)
	}
	return json.MarshalStringify(s.object)
}

// Keys returns the enumerable keys in order.
func (s *TSchema) Keys() []string { return s.object.Keys() }

// Get returns the enumerable member key.
func (s *TSchema) Get(key string) (any, bool) { return s.object.Get(key) }

// Hidden returns the hidden member name (`~kind`, `~optional`, ...): the kind as a string, true for a marker, nil for `~unsafe`.
func (s *TSchema) Hidden(name string) (any, bool) {
	value, ok := s.hidden[name]
	return value, ok
}

// Kind is the value of `~kind`, or "" for a value without one (Type.Unsafe of a plain schema).
func (s *TSchema) Kind() string {
	kind, _ := s.hidden["~kind"].(string)
	return kind
}

// Schema is the schema as the plain map an [Extension.Tool] takes. Nested members keep their order and hidden members. A Go map has no order of its own, so the map carries the
// root's key order under the member [schemaOrderKey] (a list of the keys); registering the tool writes the root in that order and leaves the member out, so the host and the provider see
// the schema as Pi's `Type` wrote it. Code that reads the map sees that one extra member.
func (s *TSchema) Schema() Schema {
	out := make(Schema, len(s.object.keys)+1)
	for _, key := range s.object.keys {
		out[key] = s.object.values[key]
	}
	out[schemaOrderKey] = slices.Clone(s.object.keys)
	if list, ok := s.hidden["~refine"].([]Refinement); ok && len(list) > 0 {
		out[schemaRefineKey] = list
	}
	if _, ok := s.hidden["~kind"].(string); ok && s.items == nil {
		// The host validates with TypeBox's kinds, as it does for a Node tool; the map carries that form of the schema (see validationSchema).
		if encoded, err := json.MarshalStringify(validationSchema(s)); err == nil {
			out[schemaValidationKey] = json.RawMessage(encoded)
		}
	}
	return out
}

// schemaOrderKey is the member of a [Schema] that lists its root keys in the order they were declared in; see [TSchema.Schema].
const schemaOrderKey = "~order"

// schemaValidationKey is the member of a [Schema] that carries the validation form of the schema (the JSON with TypeBox's `~kind` on its nodes) as raw JSON; registration sends it as
// validation_parameters and leaves it out of the parameters.
const schemaValidationKey = "~validation"

// schemaRefineKey is the member of a [Schema] that carries the refinements of the root node, which a plain map cannot hold as hidden members; see [TSchema.Schema].
const schemaRefineKey = "~refine"

// schemaRoot is the root of a tool's parameter schema as it goes on the wire: a map, written with the key order [TSchema.Schema] recorded when there is one and in
// alphabetical order otherwise (a Go map's own).
type schemaRoot map[string]any

// MarshalJSON writes the root's members in the recorded order, then any that are not in it (added after the schema was built) alphabetically; a recorded key the map no longer
// has is skipped, and the record itself is not written.
func (r schemaRoot) MarshalJSON() ([]byte, error) {
	declared, _ := r[schemaOrderKey].([]string)
	object := &OrderedObject{values: map[string]any{}}
	for _, key := range declared {
		if value, present := r[key]; present && key != schemaOrderKey && key != schemaRefineKey && key != schemaValidationKey {
			object.set(key, value)
		}
	}
	for _, key := range sortedKeys(r) {
		if _, written := object.values[key]; !written && key != schemaOrderKey && key != schemaRefineKey && key != schemaValidationKey {
			object.set(key, r[key])
		}
	}
	return json.MarshalJS(object)
}

// TypeOptions is the options object of a constructor (Pi's `options?: TSchemaOptions`): the members are appended to the schema in their order, after the ones the constructor writes,
// and replace a member of the same name in place. Build one with [NewOrderedObject]; nil is no options.
type TypeOptions = *OrderedObject

// typeNamespace is pi-ai's `Type`. Every member is a method: Type.Object, Type.String, Type.Optional, ...
type typeNamespace struct {
	// ExtendsResult is Type.ExtendsResult.
	ExtendsResult extendsResult
}

// Type is pi-ai's `Type` (TypeBox's builder namespace). The JSON Schema it returns is byte for byte what Pi's `Type` returns, member order included, which the Pi vectors in
// test/parity/sdk-vectors/pi-ai/Type.json check. Members that a later release has not ported are absent; the Codec family (Codec, Decode, Encode, DecodeBuilder, EncodeBuilder,
// IsCodec) is a numbered divergence: pig divergence (D198).
var Type typeNamespace

// jsIndexKey reports whether key is a canonical array index, which a JavaScript object orders before every other key, ascending.
func jsIndexKey(key string) (uint64, bool) {
	if key == "" || len(key) > 10 {
		return 0, false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= 1<<32-1 || strconv.FormatUint(n, 10) != key {
		return 0, false
	}
	return n, true
}

// newJSObject orders keys as a JavaScript object does: array-index keys first and ascending, the others in insertion order.
func (o *OrderedObject) jsOrder() {
	var index, other []string
	for _, key := range o.keys {
		if _, ok := jsIndexKey(key); ok {
			index = append(index, key)
		} else {
			other = append(other, key)
		}
	}
	slices.SortFunc(index, func(a, b string) int {
		left, _ := jsIndexKey(a)
		right, _ := jsIndexKey(b)
		switch {
		case left < right:
			return -1
		case left > right:
			return 1
		}
		return 0
	})
	o.keys = slices.Concat(index, other)
}

func (o *OrderedObject) clone() *OrderedObject {
	return &OrderedObject{keys: slices.Clone(o.keys), values: maps.Clone(o.values)}
}

// create is TypeBox's Memory.Create(hidden, enumerable, options): `{...enumerable, ...options}` with the hidden members attached.
func create(kind string, enumerable []any, options TypeOptions) *TSchema {
	object := NewOrderedObject(enumerable...)
	if options != nil {
		for _, key := range options.keys {
			object.set(key, options.values[key])
		}
	}
	object.jsOrder()
	return &TSchema{object: object, hidden: map[string]any{"~kind": kind}}
}

func firstOptions(options []TypeOptions) TypeOptions {
	if len(options) == 0 {
		return nil
	}
	return options[0]
}

// update is Memory.Update(current, hidden, enumerable): a copy of current with the hidden members set and the enumerable ones assigned (replaced in place, or appended).
func update(current any, hidden map[string]any, enumerable TypeOptions) *TSchema {
	var result *TSchema
	if schema, isSchema := current.(*TSchema); isSchema && schema != nil && schema.items != nil {
		current = schema.items // a schema made of an array is cloned as the array it is: its hidden members are not copied
	}
	switch current := current.(type) {
	case *TSchema:
		result = &TSchema{object: current.object.clone(), hidden: maps.Clone(current.hidden)}
	case *OrderedObject:
		result = &TSchema{object: current.clone(), hidden: map[string]any{}}
	case map[string]any:
		object := &OrderedObject{}
		for _, key := range sortedKeys(current) {
			object.set(key, current[key])
		}
		result = &TSchema{object: object, hidden: map[string]any{}}
	case []any:
		// A copy of the array, whose own `length` is a hidden member, as JavaScript's is. TypeBox's Clone of an array is `map`, which drops every non-index member, so a marker
		// set by one update is gone after the next: Type.Optional, Readonly and Immutable (two updates) leave an array with only its length, and Type.Unsafe and Type.Refine (one) keep `~unsafe` and `~refine`.
		result = &TSchema{object: &OrderedObject{}, hidden: map[string]any{"length": float64(len(current))}, items: slices.Clone(current)}
		if current == nil {
			result.items = []any{}
		}
		_, unsafe := hidden["~unsafe"]
		if _, refine := hidden["~refine"]; !unsafe && !refine {
			hidden = nil
		}
	default:
		panic(fmt.Sprintf("TypeError: cannot update a %T, a schema is an object", current))
	}
	if result.hidden == nil {
		result.hidden = map[string]any{}
	}
	for name, value := range hidden {
		result.hidden[name] = value
	}
	if enumerable != nil {
		for _, key := range enumerable.keys {
			result.object.set(key, enumerable.values[key])
		}
	}
	result.object.jsOrder()
	return result
}

func sortedKeys(m map[string]any) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	return keys
}

// IsSchema is TypeBox's IsSchema: any object (an array counts, null does not).
func (typeNamespace) IsSchema(value any) bool {
	switch value := value.(type) {
	case nil, bool, string, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return false
	default:
		reflected := reflect.ValueOf(value)
		return reflected.Kind() != reflect.Pointer || !reflected.IsNil()
	}
}

// hasMember is HasPropertyKey: an own member, enumerable or hidden.
func hasMember(value any, name string) bool {
	switch value := value.(type) {
	case *TSchema:
		if value == nil {
			return false
		}
		if _, ok := value.hidden[name]; ok {
			return true
		}
		_, ok := value.object.values[name]
		return ok
	case *OrderedObject:
		if value == nil {
			return false
		}
		_, ok := value.values[name]
		return ok
	case map[string]any:
		_, ok := value[name]
		return ok
	}
	return false
}

func memberValue(value any, name string) (any, bool) {
	switch value := value.(type) {
	case *TSchema:
		if value == nil {
			return nil, false
		}
		if hidden, ok := value.hidden[name]; ok {
			return hidden, true
		}
		member, ok := value.object.values[name]
		return member, ok
	case *OrderedObject:
		if value == nil {
			return nil, false
		}
		member, ok := value.values[name]
		return member, ok
	case map[string]any:
		member, ok := value[name]
		return member, ok
	}
	return nil, false
}

// IsKind is TypeBox's IsKind: value has `~kind` and it is kind.
func (t typeNamespace) IsKind(value any, kind string) bool {
	if !t.IsSchema(value) {
		return false
	}
	have, ok := memberValue(value, "~kind")
	return ok && have == kind
}

func (t typeNamespace) isOptionalMember(value any, name string) bool {
	return t.IsSchema(value) && hasMember(value, name)
}

// IsOptional reports whether value carries `~optional`.
func (t typeNamespace) IsOptional(value any) bool { return t.isOptionalMember(value, "~optional") }

// IsReadonly reports whether value carries `~readonly`.
func (t typeNamespace) IsReadonly(value any) bool { return t.isOptionalMember(value, "~readonly") }

// IsImmutable reports whether value carries `~immutable`.
func (t typeNamespace) IsImmutable(value any) bool { return t.isOptionalMember(value, "~immutable") }

// IsUnsafe reports whether value is a Type.Unsafe schema: an object (not an array) whose `~unsafe` is null.
func (typeNamespace) IsUnsafe(value any) bool {
	if _, isSlice := value.([]any); isSlice {
		return false
	}
	if schema, isSchema := value.(*TSchema); isSchema && schema != nil && schema.items != nil {
		return false
	}
	have, ok := memberValue(value, "~unsafe")
	return ok && have == nil
}

// IsAny, IsArray, ... are TypeBox's guards: the value is a schema of that kind.
func (t typeNamespace) IsAny(value any) bool       { return t.IsKind(value, "Any") }
func (t typeNamespace) IsArray(value any) bool     { return t.IsKind(value, "Array") }
func (t typeNamespace) IsBigInt(value any) bool    { return t.IsKind(value, "BigInt") }
func (t typeNamespace) IsBoolean(value any) bool   { return t.IsKind(value, "Boolean") }
func (t typeNamespace) IsEnum(value any) bool      { return t.IsKind(value, "Enum") }
func (t typeNamespace) IsInteger(value any) bool   { return t.IsKind(value, "Integer") }
func (t typeNamespace) IsIntersect(value any) bool { return t.IsKind(value, "Intersect") }
func (t typeNamespace) IsLiteral(value any) bool   { return t.IsKind(value, "Literal") }
func (t typeNamespace) IsNever(value any) bool     { return t.IsKind(value, "Never") }
func (t typeNamespace) IsNull(value any) bool      { return t.IsKind(value, "Null") }
func (t typeNamespace) IsNumber(value any) bool    { return t.IsKind(value, "Number") }
func (t typeNamespace) IsObject(value any) bool    { return t.IsKind(value, "Object") }
func (t typeNamespace) IsRecord(value any) bool    { return t.IsKind(value, "Record") }
func (t typeNamespace) IsRef(value any) bool       { return t.IsKind(value, "Ref") }
func (t typeNamespace) IsString(value any) bool    { return t.IsKind(value, "String") }
func (t typeNamespace) IsSymbol(value any) bool    { return t.IsKind(value, "Symbol") }
func (t typeNamespace) IsUndefined(value any) bool { return t.IsKind(value, "Undefined") }
func (t typeNamespace) IsUnion(value any) bool     { return t.IsKind(value, "Union") }
func (t typeNamespace) IsUnknown(value any) bool   { return t.IsKind(value, "Unknown") }
func (t typeNamespace) IsVoid(value any) bool      { return t.IsKind(value, "Void") }

// IsEnumValue is TypeBox's IsEnumValue: a string or a finite number.
func (typeNamespace) IsEnumValue(value any) bool {
	if _, ok := value.(string); ok {
		return true
	}
	number, ok := typeNumber(value)
	return ok && !math.IsInf(number, 0) && !math.IsNaN(number)
}

// typeNumber reads a Go number as the JavaScript number it stands for.
func typeNumber(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	}
	return 0, false
}

// Any is `Type.Any(options)`: the schema `{}`.
func (typeNamespace) Any(options ...TypeOptions) *TSchema {
	return create("Any", nil, firstOptions(options))
}

// Array is `Type.Array(items, options)`.
func (typeNamespace) Array(items any, options ...TypeOptions) *TSchema {
	return create("Array", []any{"type", "array", "items", items}, firstOptions(options))
}

// BigInt is `Type.BigInt(options)`: `{"type":"bigint"}`.
func (typeNamespace) BigInt(options ...TypeOptions) *TSchema {
	return create("BigInt", []any{"type", "bigint"}, firstOptions(options))
}

// Boolean is `Type.Boolean(options)`.
func (typeNamespace) Boolean(options ...TypeOptions) *TSchema {
	return create("Boolean", []any{"type", "boolean"}, firstOptions(options))
}

// Integer is `Type.Integer(options)`.
func (typeNamespace) Integer(options ...TypeOptions) *TSchema {
	return create("Integer", []any{"type", "integer"}, firstOptions(options))
}

// Never is `Type.Never(options)`: `{"not":{}}`.
func (typeNamespace) Never(options ...TypeOptions) *TSchema {
	return create("Never", []any{"not", NewOrderedObject()}, firstOptions(options))
}

// Null is `Type.Null(options)`.
func (typeNamespace) Null(options ...TypeOptions) *TSchema {
	return create("Null", []any{"type", "null"}, firstOptions(options))
}

// Number is `Type.Number(options)`.
func (typeNamespace) Number(options ...TypeOptions) *TSchema {
	return create("Number", []any{"type", "number"}, firstOptions(options))
}

// String is `Type.String(options)`.
func (typeNamespace) String(options ...TypeOptions) *TSchema {
	return create("String", []any{"type", "string"}, firstOptions(options))
}

// Symbol is `Type.Symbol(options)`.
func (typeNamespace) Symbol(options ...TypeOptions) *TSchema {
	return create("Symbol", []any{"type", "symbol"}, firstOptions(options))
}

// Undefined is `Type.Undefined(options)`.
func (typeNamespace) Undefined(options ...TypeOptions) *TSchema {
	return create("Undefined", []any{"type", "undefined"}, firstOptions(options))
}

// Unknown is `Type.Unknown(options)`: the schema `{}`.
func (typeNamespace) Unknown(options ...TypeOptions) *TSchema {
	return create("Unknown", nil, firstOptions(options))
}

// Void is `Type.Void(options)`.
func (typeNamespace) Void(options ...TypeOptions) *TSchema {
	return create("Void", []any{"type", "void"}, firstOptions(options))
}

// Ref is `Type.Ref(ref, options)`: `{"$ref": ref}`.
func (typeNamespace) Ref(ref string, options ...TypeOptions) *TSchema {
	return create("Ref", []any{"$ref", ref}, firstOptions(options))
}

// Union is `Type.Union(anyOf, options)`: `{"anyOf": [...]}`.
func (typeNamespace) Union(anyOf []any, options ...TypeOptions) *TSchema {
	return create("Union", []any{"anyOf", anyOf}, firstOptions(options))
}

// Intersect is `Type.Intersect(types, options)`: `{"allOf": [...]}`.
func (typeNamespace) Intersect(types []any, options ...TypeOptions) *TSchema {
	return create("Intersect", []any{"allOf", types}, firstOptions(options))
}

// Object is `Type.Object(properties, options)`. properties is an object (see [NewOrderedObject]) whose values are the property schemas; `required` lists, in the object's key order, the
// properties that are not `~optional` and is left out when there are none. Keys that are array indexes come first and ascending, as in a JavaScript object.
func (t typeNamespace) Object(properties *OrderedObject, options ...TypeOptions) *TSchema {
	if properties == nil {
		properties = NewOrderedObject()
	}
	properties = properties.clone()
	properties.jsOrder()
	required := []any{}
	for _, key := range properties.keys {
		if !t.IsOptional(properties.values[key]) {
			required = append(required, key)
		}
	}
	enumerable := []any{"type", "object"}
	if len(required) > 0 {
		enumerable = append(enumerable, "required", required)
	}
	enumerable = append(enumerable, "properties", properties)
	return create("Object", enumerable, firstOptions(options))
}

// Optional is `Type.Optional(type)`: a copy of type with the hidden `~optional`, which Type.Object reads.
func (typeNamespace) Optional(schema any) *TSchema {
	return update(schema, map[string]any{"~optional": true}, nil)
}

// Readonly is `Type.Readonly(type)`: a copy of type with the hidden `~readonly`.
func (typeNamespace) Readonly(schema any) *TSchema {
	return update(schema, map[string]any{"~readonly": true}, nil)
}

// Immutable is `Type.Immutable(type)`: a copy of type with the hidden `~immutable`.
func (typeNamespace) Immutable(schema any) *TSchema {
	return update(schema, map[string]any{"~immutable": true}, nil)
}

// Unsafe is `Type.Unsafe(schema)`: a copy of the object (or array) schema with the hidden `~unsafe` (null), for a schema TypeBox has no constructor for. Any other value panics.
func (typeNamespace) Unsafe(schema any) *TSchema {
	return update(schema, map[string]any{"~unsafe": nil}, nil)
}

// With is `Type.With(type, options)`: a copy of type with the options assigned (a Ref is not evaluated: its result is TypeBox's deferred With).
func (t typeNamespace) With(schema any, options TypeOptions) *TSchema {
	if t.IsRef(schema) {
		return deferred("With", []any{schema, orDefault(options)}, nil)
	}
	return update(schema, nil, options)
}

func orDefault(options TypeOptions) any {
	if options == nil {
		return nil
	}
	return options
}

// deferred is TypeBox's Deferred(action, parameters, options): a node for an action on a Ref, which TypeBox instantiates later.
func deferred(action string, parameters []any, options TypeOptions) *TSchema {
	if options == nil {
		options = NewOrderedObject()
	}
	return create("Deferred", []any{"type", "deferred", "action", action, "parameters", parameters, "options", options}, nil)
}

// Literal is `Type.Literal(value, options)`: `{"type": <string|number|boolean|bigint>, "const": value}`. A value that is not a string, a boolean, a number or a [BigInt] (a
// *big.Int is one too) panics, where Pi throws "Invalid Literal value"; a number is the JavaScript number it converts to. A bigint literal holds the bigint, which JSON cannot write
// (see [BigInt]).
func (typeNamespace) Literal(value any, options ...TypeOptions) *TSchema {
	var name string
	switch typed := value.(type) {
	case string:
		name = "string"
	case bool:
		name = "boolean"
	case BigInt:
		name = "bigint"
	case *big.Int:
		name, value = "bigint", NewBigInt(typed)
	default:
		number, ok := typeNumber(value)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			panic("Invalid Literal value")
		}
		name, value = "number", number
	}
	return create("Literal", []any{"type", name, "const", value}, firstOptions(options))
}

// Enum is `Type.Enum(value, options)`: `{"enum": values}`. value is a list of values, or an object in the manner of a TypeScript enum, whose values for the keys that are not
// numbers are the enum's (a TypeScript numeric enum also maps each number back to its name, which is dropped).
func (typeNamespace) Enum(value any, options ...TypeOptions) *TSchema {
	var values []any
	switch value := value.(type) {
	case *OrderedObject:
		values = []any{}
		for _, key := range value.keys {
			if jsIsNaNString(key) {
				values = append(values, value.values[key])
			}
		}
	case []any:
		values = value
	case []string:
		values = make([]any, len(value))
		for i, item := range value {
			values[i] = item
		}
	default:
		panic(fmt.Sprintf("TypeError: Type.Enum takes a list or an object, not %T", value))
	}
	return create("Enum", []any{"enum", values}, firstOptions(options))
}

// jsNumericString compiles on first use: a package-level MustCompile ran in every process that links the SDK, 139 allocations and 18 KB in init (GODEBUG=inittrace=1), and only Type.Enum over an object reads it.
var jsNumericString = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^(?:[+-]?(?:Infinity|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)|0[xX][0-9a-fA-F]+|0[oO][0-7]+|0[bB][01]+)$`)
})

// jsIsNaNString is `isNaN(key)` for a string: whether Number(key) is NaN. Number trims whitespace and reads the empty string as 0.
func jsIsNaNString(key string) bool {
	trimmed := strings.Trim(key, " \t\n\v\f\r\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
	return trimmed != "" && !jsNumericString().MatchString(trimmed)
}

const (
	integerKeyPattern = `^-?(?:0|[1-9][0-9]*)$`
	numberKeyPattern  = `^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`
	stringKeyPattern  = `^.*$`
)

// Record is `Type.Record(key, value, options)`: for a string, number or integer key, `{"type":"object","patternProperties":{<pattern>: value}}`; for a literal, boolean or
// union-of-literals key, the object with those properties. A key Pi evaluates through a part of TypeBox this SDK has not ported (an enum, an intersection or a template
// literal) is evaluated by the host's type engine.
func (t typeNamespace) Record(key, value any, options ...TypeOptions) *TSchema {
	if t.IsRef(key) {
		return deferred("Record", []any{key, value}, firstOptions(options))
	}
	return update(t.recordFromKey(key, value), nil, firstOptions(options))
}

func createRecord(pattern string, value any) *TSchema {
	return create("Record", []any{"type", "object", "patternProperties", NewOrderedObject(pattern, value)}, nil)
}

func (t typeNamespace) recordFromKey(key, value any) *TSchema {
	switch {
	case t.IsAny(key):
		return createRecord(stringKeyPattern, value)
	case t.IsBoolean(key):
		return t.Object(NewOrderedObject("true", value, "false", value))
	case t.IsEnum(key), t.IsIntersect(key), t.IsKind(key, "TemplateLiteral"):
		// The key kinds TypeBox evaluates with its type engine, which the host runs.
		return engineSchema(typeEngineCall("Record", key, value))
	case t.IsInteger(key):
		return createRecord(integerKeyPattern, value)
	case t.IsLiteral(key):
		constant, _ := memberValue(key, "const")
		switch constant := constant.(type) {
		case string:
			return t.Object(NewOrderedObject(constant, value))
		case bool:
			return t.Object(NewOrderedObject(strconv.FormatBool(constant), value))
		}
		if number, ok := typeNumber(constant); ok {
			return t.Object(NewOrderedObject(jsNumberKey(number), value))
		}
		return t.Object(NewOrderedObject())
	case t.IsNumber(key):
		return createRecord(numberKeyPattern, value)
	case t.IsUnion(key):
		anyOf, _ := memberValue(key, "anyOf")
		members := flattenUnion(anyOf)
		for _, member := range members {
			if t.IsString(member) || t.IsNumber(member) || t.IsInteger(member) {
				return createRecord(stringKeyPattern, value)
			}
		}
		properties := NewOrderedObject()
		for _, member := range members {
			if !t.IsLiteral(member) {
				continue
			}
			constant, _ := memberValue(member, "const")
			if text, ok := constant.(string); ok {
				properties.set(text, value)
			} else if number, ok := typeNumber(constant); ok {
				properties.set(jsNumberKey(number), value)
			}
		}
		return t.Object(properties)
	case t.IsString(key):
		if pattern, ok := memberValue(key, "pattern"); ok {
			if text, isString := pattern.(string); isString {
				return createRecord(text, value)
			}
		}
		return createRecord(stringKeyPattern, value)
	}
	return t.Object(NewOrderedObject())
}

// flattenUnion is TypeBox's Flatten: the members of a union, with nested unions spread in place.
func flattenUnion(anyOf any) []any {
	members, _ := anyOf.([]any)
	out := []any{}
	for _, member := range members {
		if Type.IsUnion(member) {
			nested, _ := memberValue(member, "anyOf")
			out = append(out, flattenUnion(nested)...)
		} else {
			out = append(out, member)
		}
	}
	return out
}

// jsNumberKey is String(number): the property name a JavaScript number makes.
func jsNumberKey(number float64) string {
	if number == 0 {
		return "0"
	}
	encoded, err := json.MarshalJS(number)
	if err != nil {
		return strconv.FormatFloat(number, 'g', -1, 64)
	}
	return string(encoded)
}
