package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestEmitToolResult_FindDetailsTruncationMemberKeepsPisTruncationResult ports the `truncation?: TruncationResult` member of FindToolDetails
// (core/tools/find.ts:43-46; TruncationResult core/tools/truncate.ts:15-38) through the tool_result chain of runner.ts. A replacement whose
// `truncation` is a TruncationResult keeps the typed find event with every TruncationResult member under its Pi name, and a replacement whose
// `truncation` is not an object continues unchanged as a custom event named "find".
func TestEmitToolResult_FindDetailsTruncationMemberKeepsPisTruncationResult(t *testing.T) {
	run := func(t *testing.T, details any) extension.ToolResultEvent {
		t.Helper()
		first := newFakeExtension("/first")
		first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
			return &extension.ToolResultEventResult{Details: details}, nil
		}}
		var seen extension.ToolResultEvent
		second := newFakeExtension("/second")
		second.Handlers["tool_result"] = []extension.HandlerFn{func(args ...any) (any, error) {
			seen = args[0].(extension.ToolResultEvent)
			return nil, nil
		}}
		r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
		if _, err := r.EmitToolResult(context.Background(), extension.FindToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result"}, ToolName: "find"}); err != nil {
			t.Fatal(err)
		}
		return seen
	}

	t.Run("a TruncationResult keeps the typed variant with every member", func(t *testing.T) {
		seen := run(t, map[string]any{
			"truncation": map[string]any{
				"content": "a.ts\nb.ts", "truncated": true, "truncatedBy": "lines",
				"totalLines": 2000, "totalBytes": 65536, "outputLines": 1000, "outputBytes": 32768,
				"lastLinePartial": true, "firstLineExceedsLimit": true, "maxLines": 1000, "maxBytes": 51200,
			},
			"resultLimitReached": 1000,
		})
		find, ok := seen.(extension.FindToolResultEvent)
		if !ok || find.Details == nil || find.Details.Truncation == nil {
			t.Fatalf("later handler saw %#v; want a typed find event carrying the truncation", seen)
		}
		got := *find.Details.Truncation
		want := extension.TruncationResult{
			Content: "a.ts\nb.ts", Truncated: true, TruncatedBy: "lines",
			TotalLines: 2000, TotalBytes: 65536, OutputLines: 1000, OutputBytes: 32768,
			LastLinePartial: true, FirstLineExceedsLimit: true, MaxLines: 1000, MaxBytes: 51200,
		}
		if got != want {
			t.Errorf("truncation = %+v, want %+v", got, want)
		}
		if find.Details.ResultLimitReached == nil || *find.Details.ResultLimitReached != 1000 {
			t.Errorf("resultLimitReached = %v, want 1000 beside the truncation", find.Details.ResultLimitReached)
		}
	})

	t.Run("a truncation that is not an object continues as a custom event", func(t *testing.T) {
		seen := run(t, map[string]any{"truncation": "not-an-object"})
		custom, ok := seen.(extension.CustomToolResultEvent)
		if !ok || custom.ToolName != "find" || custom.Details == nil {
			t.Fatalf("later handler saw %#v; want a custom event named find carrying the replacement", seen)
		}
	})
}
