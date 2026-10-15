package tools

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// pi: packages/durable/src/tools/edit.ts prepareEditArguments (:46-70) and isSingleEditInput (:39-43): `edits` given as a JSON string or as one edit object is repaired into an array
// (an edit needs a string oldText AND a string newText), and a top-level oldText/newText pair (both strings) is appended to the edits and removed from the arguments. The call's arguments are not changed.
// mutation-checked: `||` for `&&` in the single-edit test or in the legacy pair check fails it.
func TestPrepareEditArgumentsRepairsOnlyCompleteEdits(t *testing.T) {
	edit := map[string]any{"oldText": "a", "newText": "b"}
	for _, tc := range []struct {
		name  string
		input map[string]any
		want  map[string]any
	}{
		{"edits as one complete edit object", map[string]any{"path": "f", "edits": edit}, map[string]any{"path": "f", "edits": []any{edit}}},
		{"edits as an object with only oldText", map[string]any{"path": "f", "edits": map[string]any{"oldText": "a"}}, map[string]any{"path": "f", "edits": map[string]any{"oldText": "a"}}},
		{"edits as an object with a non-string newText", map[string]any{"edits": map[string]any{"oldText": "a", "newText": 1.0}}, map[string]any{"edits": map[string]any{"oldText": "a", "newText": 1.0}}},
		{"edits as a JSON string of one edit", map[string]any{"edits": `{"oldText":"a","newText":"b"}`}, map[string]any{"edits": []any{edit}}},
		{"edits as a JSON string of an incomplete edit", map[string]any{"edits": `{"oldText":"a"}`}, map[string]any{"edits": `{"oldText":"a"}`}},
		{"edits as a JSON string of an array", map[string]any{"edits": `[{"oldText":"a","newText":"b"}]`}, map[string]any{"edits": []any{edit}}},
		{"edits as text that is not JSON", map[string]any{"edits": "not json"}, map[string]any{"edits": "not json"}},
		{"a top-level pair becomes an edit", map[string]any{"path": "f", "oldText": "a", "newText": "b"}, map[string]any{"path": "f", "edits": []any{edit}}},
		{"a top-level pair is appended to the edits", map[string]any{"edits": []any{map[string]any{"oldText": "x", "newText": "y"}}, "oldText": "a", "newText": "b"}, map[string]any{"edits": []any{map[string]any{"oldText": "x", "newText": "y"}, edit}}},
		{"a top-level oldText alone is left in place", map[string]any{"path": "f", "oldText": "a"}, map[string]any{"path": "f", "oldText": "a"}},
		{"a top-level newText alone is left in place", map[string]any{"path": "f", "newText": "b"}, map[string]any{"path": "f", "newText": "b"}},
		{"a top-level pair with a non-string member is left in place", map[string]any{"oldText": "a", "newText": 2.0}, map[string]any{"oldText": "a", "newText": 2.0}},
	} {
		before, _ := json.Marshal(tc.input)
		got, err := prepareEditArguments(tc.input)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: prepareEditArguments = %#v, %v, want %#v", tc.name, got, err, tc.want)
		}
		if after, _ := json.Marshal(tc.input); !bytes.Equal(after, before) {
			t.Errorf("%s: the call's arguments changed from %s to %s", tc.name, before, after)
		}
	}
	if got, err := prepareEditArguments("not an object"); err != nil || got != "not an object" {
		t.Errorf("a non-object input = %#v, %v, want it returned as it is", got, err)
	}
}
