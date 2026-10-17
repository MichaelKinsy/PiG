package tools

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Each Schema call returns its own parameter map: a caller that edits one (an extension wrapping a built-in tool) does not change the next declaration, and the cached decode encodes exactly as a fresh one.
func TestBuiltInToolSchemasAreIndependentCopies(t *testing.T) {
	for _, tool := range CreateAllTools("/workspace", nil) {
		first := tool.Schema()
		want, err := json.Marshal(first)
		if err != nil {
			t.Fatal(err)
		}
		properties := first.Parameters["properties"].(map[string]any)
		for name := range properties {
			properties[name].(map[string]any)["description"] = "edited"
		}
		first.Parameters["type"] = "edited"
		got, err := json.Marshal(tool.Schema())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: after editing an earlier schema\n got %s\nwant %s", tool.Name(), got, want)
		}
	}
}
