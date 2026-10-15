package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// parseJsonWithRepair repairs text JSON.parse rejects and then parses it with no nesting limit; encoding/json stops at 10,000 levels. For a record that needs repair and nests deeper
// than that, anthropicParseJSON returns the repaired text with valid = false, so the observer's skip check validates it as it did before valid was reported.
func TestAnthropicParseJSONReportsRepairedDeepTextAsNotValid(t *testing.T) {
	const depth = 10001
	data := strings.Repeat("[", depth) + `"\q"` + strings.Repeat("]", depth)
	text, valid, err := anthropicParseJSON(data)
	if err != nil {
		t.Fatalf("anthropicParseJSON of a repairable record nested %d deep: %v", depth, err)
	}
	if text == data || !strings.Contains(text, `\\q`) {
		t.Fatalf("the record was not repaired: %.40q", text)
	}
	if json.Valid([]byte(text)) {
		t.Fatalf("the repaired text is valid for encoding/json at depth %d: the test does not exercise valid = false", depth)
	}
	if valid {
		t.Fatal("valid = true for repaired text that encoding/json rejects")
	}
}
