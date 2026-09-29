package extension

import (
	"encoding/json"
	"reflect"
	"testing"
)

// system-prompt.ts:48-64: extensions receive every collection, so an empty
// options value marshals empty arrays and objects instead of omitting them.
func TestBuildSystemPromptOptionsMarshalsCollectionComplete(t *testing.T) {
	encoded, err := json.Marshal(BuildSystemPromptOptions{Cwd: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"selectedTools": []any{}, "toolSnippets": map[string]any{}, "toolGuidelines": map[string]any{},
		"promptGuidelines": []any{}, "appendSystemPrompt": "", "sections": map[string]any{},
		"cwd": "/work", "contextFiles": []any{}, "skills": []any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire = %s, want %v", encoded, want)
	}
	populated, err := json.Marshal(BuildSystemPromptOptions{CustomPrompt: "p", SelectedTools: []string{"read"}, ToolGuidelines: map[string][]string{"read": {"g"}}, Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(populated, &got); err != nil {
		t.Fatal(err)
	}
	if got["customPrompt"] != "p" || !reflect.DeepEqual(got["selectedTools"], []any{"read"}) || !reflect.DeepEqual(got["toolGuidelines"], map[string]any{"read": []any{"g"}}) {
		t.Fatalf("populated wire = %s", populated)
	}
}
