package ai

import "testing"

// plainJSONValue has no depth bound: a deep value is proven at any depth, and only a value that contains itself takes the encode/decode path.
func TestDecodedFormBlockDeepAndCyclicArguments(t *testing.T) {
	deep := map[string]any{"leaf": 1.0}
	for range 1000 {
		deep = map[string]any{"next": deep, "list": []any{"x"}}
	}
	if _, ok := DecodedFormBlock(ToolCall{ID: "call", Name: "tool", Arguments: map[string]any{"deep": deep}}); !ok {
		t.Fatal("a 1,000-level argument object is not proven")
	}

	cyclicMap := map[string]any{"a": 1.0}
	cyclicMap["self"] = cyclicMap
	if _, ok := DecodedFormBlock(ToolCall{ID: "call", Name: "tool", Arguments: map[string]any{"m": cyclicMap}}); ok {
		t.Fatal("a map that contains itself is proven")
	}
	root := map[string]any{}
	root["root"] = []any{root}
	if _, ok := DecodedFormBlock(ToolCall{ID: "call", Name: "tool", Arguments: root}); ok {
		t.Fatal("arguments that contain themselves are proven")
	}
	cyclicSlice := []any{1.0, nil}
	cyclicSlice[1] = cyclicSlice
	if _, ok := DecodedFormBlock(ToolCall{ID: "call", Name: "tool", Arguments: map[string]any{"s": cyclicSlice}}); ok {
		t.Fatal("a slice that contains itself is proven")
	}
	shared := []any{"x"}
	if _, ok := DecodedFormBlock(ToolCall{ID: "call", Name: "tool", Arguments: map[string]any{"a": shared, "b": shared}}); !ok {
		t.Fatal("a value shared by two members is not a cycle")
	}
}
