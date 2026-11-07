package runtime

import (
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports apps/p2/src/runtime/schema.ts: schema helpers with the exact JSON TypeBox's `Type.Literal`, `Type.Record` and
// `Type.Unsafe` built, so contracts, state and tool parameters keep their JSON and their Chord fingerprints.
//
// `literal(value)` is `{ type: typeof value, const: value }` and `record(value)` is `{ type: "object",
// patternProperties: { "^.*$": value } }`: Chord's own `contracts.Lit` and `contracts.Map` build exactly those, so
// p2's contracts call them directly. `unsafe` only retypes a schema (a TypeScript type-level cast) and has no Go form.
// What is left is `propertiesOf`.

// PropertiesOf returns the property schemas of an object schema, optional ones marked with Opt again, for spreading
// into another Obj (TypeBox's `schema.properties`): a property listed in the schema's `required` is a plain schema.
func PropertiesOf(schema contracts.Schema) []contracts.Property {
	node, _ := schema.Node.(*delta.JsonObject)
	if node == nil {
		return nil
	}
	properties, _ := node.Value("properties").(*delta.JsonObject)
	if properties == nil {
		return nil
	}
	var required []string
	if list, ok := node.Value("required").([]any); ok {
		for _, name := range list {
			if text, ok := name.(string); ok {
				required = append(required, text)
			}
		}
	}
	result := make([]contracts.Property, 0, properties.Len())
	for name, value := range properties.All() {
		property := contracts.Schema{Node: value}
		if !slices.Contains(required, name) {
			property = contracts.Opt(property)
		}
		result = append(result, contracts.Prop(name, property))
	}
	return result
}
