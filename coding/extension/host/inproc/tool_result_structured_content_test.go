package inproc_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func toolResultChain(t *testing.T, base extension.ToolResultEventBase, results ...*extension.ToolResultEventResult) (seen []extension.ToolResultEventBase, final *extension.ToolResultEventResult) {
	t.Helper()
	exts := make([]extension.Extension, len(results))
	for i, result := range results {
		exts[i] = extension.Extension{Path: "/ext/" + string(rune('a'+i)) + ".ts", Handlers: map[string][]extension.HandlerFn{
			"tool_result": {func(args ...any) (any, error) {
				seen = append(seen, args[0].(extension.CustomToolResultEvent).ToolResultEventBase)
				return result, nil
			}},
		}}
	}
	runner := inproc.NewRunner(exts, t.TempDir())
	final, err := runner.EmitToolResult(t.Context(), extension.CustomToolResultEvent{ToolResultEventBase: base, ToolName: "structured"})
	if err != nil {
		t.Fatal(err)
	}
	return seen, final
}

func structuredBase() extension.ToolResultEventBase {
	return extension.ToolResultEventBase{
		Type: "tool_result", ToolCallID: "call/1", ParentToolCallID: "call",
		Input:             map[string]any{},
		Content:           []any{map[string]any{"type": "text", "text": "original"}},
		StructuredContent: json.RawMessage(`{"n":1}`),
	}
}

// Upstream runner.ts:1184-1189: replacing `content` without returning `structuredContent` drops the structured content, which may no longer match. The next handler sees the event without it.
func TestEmitToolResultDropsStructuredContentWhenContentIsReplaced(t *testing.T) {
	replaced := []any{map[string]any{"type": "text", "text": "redacted"}}
	seen, final := toolResultChain(t, structuredBase(),
		&extension.ToolResultEventResult{Content: replaced},
		&extension.ToolResultEventResult{},
	)
	if len(seen) != 2 {
		t.Fatalf("handlers ran %d times, want 2", len(seen))
	}
	if string(seen[0].StructuredContent) != `{"n":1}` {
		t.Fatalf("first handler saw structuredContent %s", seen[0].StructuredContent)
	}
	if seen[1].StructuredContent != nil {
		t.Fatalf("second handler still saw structuredContent %s after the content was replaced", seen[1].StructuredContent)
	}
	if final == nil || final.StructuredContent != nil || !reflect.DeepEqual(final.Content, replaced) {
		t.Fatalf("final = %+v", final)
	}
	// runner.ts:1046-1048 in nested calls: the parent id survives the chain.
	if seen[1].ParentToolCallID != "call" {
		t.Fatalf("parentToolCallId lost: %q", seen[1].ParentToolCallID)
	}
}

// Upstream runner.ts:1184-1196: returning `structuredContent` along with `content` keeps it, and `structuredContent` alone is a modification.
func TestEmitToolResultKeepsStructuredContentReturnedWithContent(t *testing.T) {
	replaced := []any{map[string]any{"type": "text", "text": "redacted"}}
	_, final := toolResultChain(t, structuredBase(), &extension.ToolResultEventResult{Content: replaced, StructuredContent: json.RawMessage(`{"n":"redacted"}`)})
	if final == nil || string(final.StructuredContent) != `{"n":"redacted"}` {
		t.Fatalf("final = %+v", final)
	}

	seen, final := toolResultChain(t, structuredBase(),
		&extension.ToolResultEventResult{StructuredContent: json.RawMessage(`{"n":2}`)},
		&extension.ToolResultEventResult{},
	)
	if final == nil || string(final.StructuredContent) != `{"n":2}` || string(seen[1].StructuredContent) != `{"n":2}` {
		t.Fatalf("structuredContent alone: seen=%+v final=%+v", seen, final)
	}
	if !reflect.DeepEqual(final.Content, structuredBase().Content) {
		t.Fatalf("content changed: %+v", final.Content)
	}
}

// Upstream runner.ts:1170-1179 and 1224-1231: with no handler result the runner returns undefined, and an unmodified event never reports structured content.
func TestEmitToolResultWithoutModificationReturnsNothing(t *testing.T) {
	_, final := toolResultChain(t, structuredBase(), nil)
	if final != nil {
		t.Fatalf("final = %+v, want nil", final)
	}
}
