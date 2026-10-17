package jsonschema

import "testing"

// An element after prefixItems that fails items is reported at its own index.
func TestItemsAfterPrefixItemsReportTheElementIndex(t *testing.T) {
	compiler := NewCompiler()
	if err := compiler.AddResource("schema.json", map[string]any{"prefixItems": []any{map[string]any{"type": "string"}}, "items": false}); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	failure, ok := schema.Validate([]any{"a", 1.0, 2.0}).(*ValidationError)
	if !ok {
		t.Fatal("the extra elements validated")
	}
	var locations []string
	var walk func(*ValidationError)
	walk = func(node *ValidationError) {
		if len(node.Causes) == 0 {
			locations = append(locations, "/"+joinLocation(node.InstanceLocation))
		}
		for _, cause := range node.Causes {
			walk(cause)
		}
	}
	walk(failure)
	if len(locations) != 2 || locations[0] != "/1" || locations[1] != "/2" {
		t.Fatalf("locations %v, want /1 and /2", locations)
	}
}

func joinLocation(location []string) string {
	out := ""
	for index, segment := range location {
		if index > 0 {
			out += "/"
		}
		out += segment
	}
	return out
}
