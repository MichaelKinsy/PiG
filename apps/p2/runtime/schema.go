package runtime

import (
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/schema.ts: schema helpers with the exact JSON TypeBox's `Type.Literal`, `Type.Record` and
// `Type.Unsafe` built, so contracts, state and tool parameters keep their JSON and their Chord fingerprints. p2's
// schemas are written with PiG's pi-schema port (package schema) and these helpers, as upstream writes them with
// `@earendil-works/pi-schema`; Contract hands one to Chord.

// Literal is `Type.Literal(value)`: the value with its JSON type (pi-schema's `lit` has no `type`). value is a string,
// a number (int or float64) or a bool.
func Literal[V string | bool | int | float64](value V) schema.Schema {
	switch typed := any(value).(type) {
	case string:
		return chordjson.ObjectOf("type", "string", "const", typed)
	case bool:
		return chordjson.ObjectOf("type", "boolean", "const", typed)
	case int:
		return chordjson.ObjectOf("type", "number", "const", float64(typed))
	default:
		return chordjson.ObjectOf("type", "number", "const", any(value).(float64))
	}
}

// Record is `Type.Record(Type.String(), value)`: one pattern property for every key (pi-schema's `map` uses
// `additionalProperties`).
func Record(value schema.Schema) schema.Schema {
	return chordjson.ObjectOf("type", "object", "patternProperties", chordjson.ObjectOf("^.*$", value))
}

// Contract is a pi-schema schema as a Chord contract schema (the same JSON).
func Contract(s schema.Schema) contracts.Schema { return contracts.Schema{Node: s} }

// StateProp is a Chord contract property whose schema is a pi-schema schema.
func StateProp(name string, s schema.Schema) contracts.Property {
	return contracts.Prop(name, Contract(s))
}

// check compiles a schema for repeated `compile(schema).check` calls; the schemas here are constants, so a compile
// error is a programming error.
func check(s schema.Schema) func(value any) bool {
	validator, err := schema.Compile(s, schema.CompileOptions{})
	if err != nil {
		panic(err)
	}
	return func(value any) bool {
		ok, err := validator.Check(value)
		return err == nil && ok
	}
}

// PropertiesOf returns the property schemas of an object schema, optional ones marked optional again, for spreading
// into another schema.Obj (TypeBox's `schema.properties`): a property listed in the schema's `required` is required.
func PropertiesOf(s schema.Schema) schema.Properties {
	properties, _ := s.Value("properties").(*chordjson.Object)
	if properties == nil {
		return nil
	}
	var required []string
	if list, ok := s.Value("required").([]any); ok {
		for _, name := range list {
			if text, ok := name.(string); ok {
				required = append(required, text)
			}
		}
	}
	result := make(schema.Properties, 0, properties.Len())
	for name, value := range properties.All() {
		property, _ := value.(*chordjson.Object)
		result = append(result, schema.Property{Name: name, Schema: property, Optional: !slices.Contains(required, name)})
	}
	return result
}
