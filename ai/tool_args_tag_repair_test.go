package ai

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func captureRepairs(t *testing.T) func() []ToolArgumentsRepair {
	t.Helper()
	var mu sync.Mutex
	var got []ToolArgumentsRepair
	SetToolArgumentsRepairHook(func(r ToolArgumentsRepair) { mu.Lock(); got = append(got, r); mu.Unlock() })
	t.Cleanup(func() { SetToolArgumentsRepairHook(nil) })
	return func() []ToolArgumentsRepair {
		mu.Lock()
		defer mu.Unlock()
		return append([]ToolArgumentsRepair(nil), got...)
	}
}

// Both edit properties lost `: "` in observed malformed tool calls.
const tagArrowEdit = `{"edits": [{"newText">        class SwappingEntry:\n            def __init__(self, entry: tuple[Any, ...]) -> None:\n                self._entry = entry\n", "oldText">        class SwappingEntry(tuple[Any, ...]):\n            def __getitem__(self, index: int) -> Any:\n                return 1\n"}], "path": "/w/tests/test_cache.py"}`

func TestTagArrowEditIsRepaired(t *testing.T) {
	repairs := captureRepairs(t)
	call := ParseToolCallArguments(tagArrowEdit)
	if call.ArgumentsError != "" {
		t.Fatalf("not repaired: %s", call.ArgumentsError)
	}
	edits, _ := call.Arguments["edits"].([]any)
	if len(edits) != 1 || call.Arguments["path"] != "/w/tests/test_cache.py" {
		t.Fatalf("arguments = %#v", call.Arguments)
	}
	edit := edits[0].(map[string]any)
	if !strings.HasPrefix(edit["newText"].(string), "        class SwappingEntry:\n") || !strings.Contains(edit["oldText"].(string), "-> Any:\n") {
		t.Fatalf("edit = %#v", edit)
	}
	want := []ToolArgumentsRepair{{Kind: "tag-arrow", Count: 2, Keys: []string{"newText", "oldText"}}}
	if got := repairs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("repairs = %#v, want %#v", got, want)
	}
}

func TestTagArrowSingleAndContent(t *testing.T) {
	repairs := captureRepairs(t)
	call := ParseToolCallArguments(`{"path": "a.py", "edits": [{"oldText": "x -> y", "newText">x => y"}]}`)
	if call.ArgumentsError != "" || call.Arguments["edits"].([]any)[0].(map[string]any)["newText"] != "x => y" {
		t.Fatalf("single: %#v %s", call.Arguments, call.ArgumentsError)
	}
	call = ParseToolCallArguments(`{"path": "b.go", "content">package b\n"}`)
	if call.ArgumentsError != "" || call.Arguments["content"] != "package b\n" {
		t.Fatalf("content: %#v %s", call.Arguments, call.ArgumentsError)
	}
	if n := len(repairs()); n != 2 {
		t.Fatalf("repairs = %d, want 2", n)
	}
}

func TestTagArrowLeavesValidAndOtherBrokenJSONAlone(t *testing.T) {
	repairs := captureRepairs(t)
	// Valid JSON whose string mentions the pattern is parsed as is.
	valid := `{"path": "x", "edits": [{"oldText": "{\"oldText\">", "newText": "y"}]}`
	call := ParseToolCallArguments(valid)
	if call.ArgumentsError != "" || call.Arguments["edits"].([]any)[0].(map[string]any)["oldText"] != `{"oldText">` {
		t.Fatalf("valid: %#v %s", call.Arguments, call.ArgumentsError)
	}
	// Other properties and other corruptions keep the original error.
	for _, text := range []string{
		`{"path">"x"}`,
		`{"edits": [{"oldText": "a" "newText": "b"}]}`,
		`{"edits": [{"oldText">a", "newText": "b"]}`, // repair alone cannot fix the bracket
	} {
		call := ParseToolCallArguments(text)
		if call.ArgumentsError == "" || !strings.Contains(call.ArgumentsError, "Received arguments:\n"+text) {
			t.Fatalf("%s: want original error, got %#v %q", text, call.Arguments, call.ArgumentsError)
		}
	}
	if got := repairs(); len(got) != 0 {
		t.Fatalf("unexpected repairs %#v", got)
	}
}
