package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestEmitToolResult_FindDetailsKeepTheTypedVariantOnlyWhenTheyFit ports FindToolDetails (core/tools/find.ts:43, `{ truncation?, resultLimitReached?:
// number }`) through the tool_result chain of runner.ts: a replacement that fits the shape keeps the typed find event with resultLimitReached
// present (0 stays distinct from absent), and a replacement carrying anything else continues unchanged as a custom event with toolName "find".
func TestEmitToolResult_FindDetailsKeepTheTypedVariantOnlyWhenTheyFit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		details   any
		wantLimit *float64
		wantTyped bool
	}{
		{"limit reached", map[string]any{"resultLimitReached": 1000}, new(1000.0), true},
		{"zero limit stays present", map[string]any{"resultLimitReached": 0}, new(0.0), true},
		{"empty details", map[string]any{}, nil, true},
		{"unknown field", map[string]any{"resultLimitReached": 5, "extra": "x"}, nil, false},
		{"wrong field type", map[string]any{"resultLimitReached": "many"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := newFakeExtension("/first")
			first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
				return &extension.ToolResultEventResult{Details: tc.details}, nil
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
			if !tc.wantTyped {
				custom, ok := seen.(extension.CustomToolResultEvent)
				if !ok || custom.ToolName != "find" || custom.Details == nil {
					t.Fatalf("later handler saw %#v; want a custom event named find carrying the replacement", seen)
				}
				return
			}
			find, ok := seen.(extension.FindToolResultEvent)
			if !ok || find.Details == nil {
				t.Fatalf("later handler saw %#v; want a typed find event", seen)
			}
			if (find.Details.ResultLimitReached == nil) != (tc.wantLimit == nil) || (tc.wantLimit != nil && *find.Details.ResultLimitReached != *tc.wantLimit) {
				t.Fatalf("resultLimitReached = %v, want %v", find.Details.ResultLimitReached, tc.wantLimit)
			}
		})
	}
}
