package host

// The JSON Schema data pi-schema's builders emit, for tests (builders.ts): an object lists `properties` and then
// `required` (only when non-empty), and a test object is open, as Pi's tests write `obj(properties, { open: true })`.

import "github.com/MichaelKinsy/PiG/internal/chord/chordjson"

// kw is a schema written as keyword and value pairs, in order.
func kw(pairs ...any) Schema { return chordjson.ObjectOf(pairs...) }

func tOptional(schema Schema) optional { return opt(schema) }

// tObject is `obj(properties, { open: true })` over name, schema pairs in order; a Schema among the pairs is an option
// (keywords added after the properties).
func tObject(pairs ...any) Schema {
	props := chordjson.NewObject(len(pairs) / 2)
	var required []any
	var options []Schema
	for index := 0; index < len(pairs); index++ {
		if option, ok := pairs[index].(Schema); ok {
			options = append(options, option)
			continue
		}
		key := pairs[index].(string)
		index++
		if wrapped, ok := pairs[index].(optional); ok {
			props.Set(key, wrapped.schema)
			continue
		}
		props.Set(key, pairs[index])
		required = append(required, key)
	}
	schema := chordjson.ObjectOf("type", "object", "properties", props)
	if len(required) > 0 {
		schema.Set("required", required)
	}
	return with(schema, options)
}

func with(schema Schema, options []Schema) Schema {
	for _, option := range options {
		for key, value := range option.All() {
			schema.Set(key, value)
		}
	}
	return schema
}

func tString(options ...Schema) Schema  { return with(kw("type", "string"), options) }
func tNumber(options ...Schema) Schema  { return with(kw("type", "number"), options) }
func tBoolean(options ...Schema) Schema { return with(kw("type", "boolean"), options) }
func tNull() Schema                     { return kw("type", "null") }
func tVoid() Schema                     { return kw("type", "void") }
func tUnknown() Schema                  { return kw() }

// tTypedLiteral is fixtures/schemas.ts `typedLit(value)`: the literal with its JSON type, as TypeBox's `Type.Literal`.
func tTypedLiteral(value any) Schema {
	switch value.(type) {
	case string:
		return kw("type", "string", "const", value)
	case bool:
		return kw("type", "boolean", "const", value)
	}
	return kw("type", "number", "const", value)
}

// tTypedRecord is fixtures/schemas.ts `record(value)`: one pattern property for every key, as TypeBox's `Type.Record`.
func tTypedRecord(value Schema) Schema {
	return kw("type", "object", "patternProperties", kw("^.*$", value))
}

func tArray(items Schema, options ...Schema) Schema {
	return with(kw("type", "array", "items", items), options)
}

func tFunction(parameters []Schema, returns Schema) Schema {
	out := make([]any, len(parameters))
	for index, parameter := range parameters {
		out[index] = parameter
	}
	return kw("type", "function", "parameters", out, "returnType", returns)
}
