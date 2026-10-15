package host

// The JSON Schema data TypeBox 1.3.27's Type builders emit, for tests (Object writes "required" only when non-empty).

import "maps"

type optional struct{ schema Schema }

func tOptional(schema Schema) optional { return optional{schema} }

// tObject is Type.Object over name, schema pairs in order: "required" keeps that order.
func tObject(pairs ...any) Schema {
	props := Schema{}
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
			props[key] = wrapped.schema
			continue
		}
		props[key] = pairs[index]
		required = append(required, key)
	}
	schema := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return with(schema, options)
}

func with(schema Schema, options []Schema) Schema {
	for _, option := range options {
		maps.Copy(schema, option)
	}
	return schema
}

func tString(options ...Schema) Schema  { return with(Schema{"type": "string"}, options) }
func tNumber(options ...Schema) Schema  { return with(Schema{"type": "number"}, options) }
func tBoolean(options ...Schema) Schema { return with(Schema{"type": "boolean"}, options) }
func tNull() Schema                     { return Schema{"type": "null"} }
func tVoid() Schema                     { return Schema{"type": "void"} }
func tUnknown() Schema                  { return Schema{} }
func tLiteral(value any) Schema {
	switch value.(type) {
	case string:
		return Schema{"const": value, "type": "string"}
	case bool:
		return Schema{"const": value, "type": "boolean"}
	}
	return Schema{"const": value, "type": "number"}
}
func tArray(items Schema, options ...Schema) Schema {
	return with(Schema{"type": "array", "items": items}, options)
}
func tFunction(parameters []Schema, returns Schema) Schema {
	out := make([]any, len(parameters))
	for index, parameter := range parameters {
		out[index] = parameter
	}
	return Schema{"type": "function", "parameters": out, "returnType": returns}
}
