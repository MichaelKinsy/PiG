package tools

import (
	"reflect"
	"testing"
)

// Pi packages/durable/src/tools/edit.ts:35 EditToolInput (path and edits) and :49 prepareEditArguments: the validated input keeps the path and every replacement, and an input without a replacement is rejected.
func TestEditToolInputIsPathAndEdits(t *testing.T) {
	input, err := validateEditInput(map[string]any{"path": "a.txt", "edits": []any{map[string]any{"oldText": "x", "newText": "y"}}})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(input) != reflect.TypeFor[EditToolInput]() {
		t.Fatalf("validated input is %T, want EditToolInput", input)
	}
	typed := input
	if typed.Path != "a.txt" || len(typed.Edits) != 1 || typed.Edits[0].OldText != "x" || typed.Edits[0].NewText != "y" {
		t.Fatalf("input = %+v", typed)
	}
	if _, err := validateEditInput(map[string]any{"path": "a.txt", "edits": []any{}}); err == nil {
		t.Fatal("an empty edits list must be rejected")
	}
}
