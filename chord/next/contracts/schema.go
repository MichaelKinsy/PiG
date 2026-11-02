package contracts

import (
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/contracts/schema.ts (short constructors for TypeBox schemas, the bytes leaf, and
// method declarations).
//
// Forced Go differences. TSchema is TypeBox's schema as data: the node (an ordered *delta.JsonObject, or a bool) plus
// the non-enumerable "~optional" marker, which is the Optional field. The generic Static<>, Defaulted<> and Bounds
// types are type-level only and designed out; bounds and defaults are Option values (Min, Max, Pattern, Default),
// and a constructor panics on an option that does not apply to it, where TypeScript rejects it at compile time.
// withBytesCheck's `~refine` is designed out: a Go value carries no executable check, so the bytes marker is checked
// natively by type (CheckBytes).

// Entry is one name and value of an object literal whose key order matters: Go has no ordered literal.
type Entry[T any] struct {
	Name  string
	Value T
}

// Property is one property of an object schema, or one field of params.
type Property = Entry[TSchema]

// Prop is `{ [name]: schema }`.
func Prop(name string, schema TSchema) Property { return Property{Name: name, Value: schema} }

// Params is a flat record of required fields, in identity order (see ReadParams).
type Params []Property

// TSchema is a TypeBox schema: JSON Schema data. Node is an ordered *delta.JsonObject or a bool; the zero TSchema
// (a nil Node) is no schema, upstream's `undefined`.
type TSchema struct {
	Node delta.JsonValue
	// Optional is TypeBox's non-enumerable "~optional": an object field made optional by Opt.
	Optional bool
}

func (TSchema) localMember() {}

// Option is a bound or a default for a schema constructor.
type Option struct {
	name  string
	apply func(*options)
}

type options struct {
	min, max   *float64
	pattern    *string
	fallback   any
	hasDefault bool
}

// Min is the lower bound of a length, a number or an array length (`min`).
func Min(n float64) Option { return Option{"Min", func(o *options) { o.min = &n }} }

// Max is the upper bound of a length, a number or an array length (`max`).
func Max(n float64) Option { return Option{"Max", func(o *options) { o.max = &n }} }

// Pattern is the regular expression a string matches (`pattern`).
func Pattern(pattern string) Option {
	return Option{"Pattern", func(o *options) { o.pattern = &pattern }}
}

// Default is the declared default value (`default`): a string for Str, a number for Num and Int, a bool for Bool.
func Default(value any) Option {
	return Option{"Default", func(o *options) { o.fallback, o.hasDefault = value, true }}
}

// read applies the options of constructor, which accepts the named ones; defaults are checked by kind.
func read(constructor string, allowed []string, kind func(any) (any, bool), opts []Option) options {
	var out options
	for _, option := range opts {
		ok := false
		for _, name := range allowed {
			ok = ok || name == option.name
		}
		if !ok {
			panic(fmt.Sprintf("contracts: %s does not take the option %s", constructor, option.name))
		}
		option.apply(&out)
	}
	if out.hasDefault {
		value, ok := kind(out.fallback)
		if !ok {
			panic(fmt.Sprintf("contracts: %s: the default %v has the wrong type", constructor, out.fallback))
		}
		out.fallback = value
	}
	return out
}

func stringKind(value any) (any, bool) {
	text, ok := value.(string)
	return text, ok
}

func numberKind(value any) (any, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	}
	return nil, false
}

func boolKind(value any) (any, bool) {
	flag, ok := value.(bool)
	return flag, ok
}

var (
	lengthOptions = []string{"Min", "Max"}
	stringOptions = []string{"Min", "Max", "Pattern", "Default"}
	numberOptions = []string{"Min", "Max", "Default"}
	boolOptions   = []string{"Default"}
)

// setLimits writes the bounds under their JSON Schema names; an absent bound is left out.
func setLimits(node *delta.JsonObject, o options, lower, upper string) {
	if o.min != nil {
		node.Set(lower, *o.min)
	}
	if o.max != nil {
		node.Set(upper, *o.max)
	}
}

func setDefault(node *delta.JsonObject, o options) {
	if o.hasDefault {
		node.Set("default", o.fallback)
	}
}

// Str is a string, with optional length bounds, pattern and default.
func Str(opts ...Option) TSchema {
	o := read("Str", stringOptions, stringKind, opts)
	node := delta.JsonObjectOf("type", "string")
	setLimits(node, o, "minLength", "maxLength")
	if o.pattern != nil {
		node.Set("pattern", *o.pattern)
	}
	setDefault(node, o)
	return TSchema{Node: node}
}

// Num is a finite number within optional bounds, with an optional default.
func Num(opts ...Option) TSchema {
	o := read("Num", numberOptions, numberKind, opts)
	node := delta.JsonObjectOf("type", "number")
	setLimits(node, o, "minimum", "maximum")
	setDefault(node, o)
	return TSchema{Node: node}
}

// Int is an integer within optional bounds, with an optional default.
func Int(opts ...Option) TSchema {
	o := read("Int", numberOptions, numberKind, opts)
	node := delta.JsonObjectOf("type", "integer")
	setLimits(node, o, "minimum", "maximum")
	setDefault(node, o)
	return TSchema{Node: node}
}

// Bool is a boolean, with an optional default.
func Bool(opts ...Option) TSchema {
	o := read("Bool", boolOptions, boolKind, opts)
	node := delta.JsonObjectOf("type", "boolean")
	setDefault(node, o)
	return TSchema{Node: node}
}

// Nul is `null`; also the omitted side of a method.
func Nul() TSchema { return TSchema{Node: delta.JsonObjectOf("type", "null")} }

// Literal is the Go form of `string | number | boolean`.
type Literal interface {
	~string | ~bool | ~int | ~int64 | ~float64
}

// literalSchema is Type.Literal(value): its type keyword and const.
func literalSchema[V Literal](value V) TSchema {
	switch typed := any(value).(type) {
	case string:
		return TSchema{Node: delta.JsonObjectOf("type", "string", "const", typed)}
	case bool:
		return TSchema{Node: delta.JsonObjectOf("type", "boolean", "const", typed)}
	}
	var number float64
	switch typed := any(value).(type) {
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case float64:
		number = typed
	}
	return TSchema{Node: delta.JsonObjectOf("type", "number", "const", number)}
}

// Lit is exactly the given value.
func Lit[V Literal](value V) TSchema { return literalSchema(value) }

// OneOf is one of the given literals: a union of one literal schema per value, in order.
func OneOf[V Literal](values ...V) TSchema {
	schemas := make([]TSchema, len(values))
	for i, value := range values {
		schemas[i] = literalSchema(value)
	}
	return Union(schemas)
}

// Union is any of the given schemas.
func Union(schemas []TSchema) TSchema {
	nodes := make([]any, len(schemas))
	for i, schema := range schemas {
		nodes[i] = schema.Node
	}
	return TSchema{Node: delta.JsonObjectOf("anyOf", nodes)}
}

// Obj is a closed object: unknown fields are refused. Properties are read in the order JavaScript enumerates an
// object's own keys; a field made optional by Opt is left out of `required`.
func Obj(properties ...Property) TSchema {
	ordered := orderedEntries(properties)
	props := delta.NewJsonObject(len(ordered))
	var required []any
	for _, property := range ordered {
		props.Set(property.Name, property.Value.Node)
		if !property.Value.Optional {
			required = append(required, property.Name)
		}
	}
	node := delta.JsonObjectOf("type", "object")
	if len(required) > 0 {
		node.Set("required", required)
	}
	node.Set("properties", props)
	node.Set("additionalProperties", false)
	return TSchema{Node: node}
}

// Arr is an array, with optional length bounds.
func Arr(items TSchema, opts ...Option) TSchema {
	o := read("Arr", lengthOptions, nil, opts)
	node := delta.JsonObjectOf("type", "array", "items", items.Node)
	setLimits(node, o, "minItems", "maxItems")
	return TSchema{Node: node}
}

// Map is a string-keyed record.
func Map(value TSchema) TSchema {
	return TSchema{Node: delta.JsonObjectOf("type", "object", "patternProperties", delta.JsonObjectOf("^.*$", value.Node))}
}

// Opt is an optional object field.
func Opt(schema TSchema) TSchema {
	schema.Optional = true
	return schema
}

// Unknown is any value.
func Unknown() TSchema { return TSchema{Node: delta.NewJsonObject(0)} }

// Bytes is a `Uint8Array` leaf, a []byte in Go, checked by CheckBytes and never enumerated. Its enumerable form is the
// data marker `{ type: "bytes", minLength?, maxLength? }`.
func Bytes(opts ...Option) TSchema {
	o := read("Bytes", lengthOptions, nil, opts)
	node := delta.JsonObjectOf("type", "bytes")
	setLimits(node, o, "minLength", "maxLength")
	return TSchema{Node: node}
}

// CheckBytes is the check of a bytes schema: value is a []byte whose length is within the marker's bounds. A schema that
// is not a bytes marker matches nothing.
func CheckBytes(schema TSchema, value any) bool {
	node, ok := schema.Node.(*delta.JsonObject)
	if !ok || node.Value("type") != "bytes" {
		return false
	}
	data, ok := value.([]byte)
	if !ok {
		return false
	}
	lower, upper := 0.0, math.Inf(1)
	if bound, ok := node.Value("minLength").(float64); ok {
		lower = bound
	}
	if bound, ok := node.Value("maxLength").(float64); ok {
		upper = bound
	}
	return float64(len(data)) >= lower && float64(len(data)) <= upper
}

// Method is a method declaration: its argument and result schemas; an omitted side is `null` on the wire.
type Method struct {
	Args   TSchema
	Result TSchema
}

func (*Method) localMember() {}

// KeyedMember is a keyed state member's declaration (slice 6a §12): documents of Schema by string key; only in local
// contracts.
type KeyedMember struct {
	Schema TSchema
}

func (*KeyedMember) localMember() {}

// Keyed declares a keyed state member of a local contract: documents of schema by string key, each watchable.
func Keyed(schema TSchema) *KeyedMember { return &KeyedMember{Schema: schema} }

// NewMethod is `method(args?, result?)` (upstream's `method`, which Go cannot name beside the Method type): no side,
// `args`, or `args, result`. A side that is the zero TSchema is omitted, as `method(undefined, result)` omits the
// arguments, and is `null`.
func NewMethod(sides ...TSchema) *Method {
	if len(sides) > 2 {
		panic("contracts: NewMethod takes at most an argument and a result schema")
	}
	method := &Method{Args: Nul(), Result: Nul()}
	if len(sides) > 0 && sides[0].Node != nil {
		method.Args = sides[0]
	}
	if len(sides) > 1 && sides[1].Node != nil {
		method.Result = sides[1]
	}
	return method
}

// LocalMember is a local contract's member: a state schema (TSchema), a *Method or a *KeyedMember.
type LocalMember interface {
	localMember()
}

// orderedEntries returns entries as JavaScript would enumerate the same object literal: array-index names first in
// ascending order, then the others in the given order; a repeated name keeps its first place and takes its last value.
func orderedEntries[T any](entries []Entry[T]) []Entry[T] {
	out := make([]Entry[T], 0, len(entries))
	at := map[string]int{}
	for _, entry := range entries {
		if i, seen := at[entry.Name]; seen {
			out[i].Value = entry.Value
			continue
		}
		at[entry.Name] = len(out)
		out = append(out, entry)
	}
	ownKeyOrder(out, func(entry Entry[T]) string { return entry.Name })
	return out
}
