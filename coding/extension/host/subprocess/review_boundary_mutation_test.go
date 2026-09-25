package subprocess

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// extensions-runner.test.ts retains shared draft mutations even on error.
func TestReviewBoundaryMutationThroughFusedSDK(t *testing.T) {
	for _, outcome := range []string{"return", "error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			ext := sdk.New("boundary-review")
			ext.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) {
				entries := data["entries"].([]any)
				data["entries"] = append(entries, map[string]any{"type": "custom", "customType": "kept"})
				if outcome == "error" {
					// A failed handler's explicit result is ignored, but its input
					// mutations still reach the next preview and handler.
					return map[string]any{"entries": []any{}, "continue": true}, fmt.Errorf("boundary failed")
				}
				if outcome == "panic" {
					panic("boundary failed")
				}
				return nil, nil
			})
			host := NewHost(t.TempDir())
			defer host.Shutdown("test complete")
			loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "boundary-review", Enabled: true}, ext.RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
			defer runner.Invalidate("test complete")
			var handlerErrors []*extension.ExtensionError
			runner.AddErrorListener(func(err *extension.ExtensionError) { handlerErrors = append(handlerErrors, err) })
			result, err := runner.EmitBoundary(t.Context(), "agent_before_settle", extension.AgentActivityCompleted,
				func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
					return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries))}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Entries) != 1 || result.Entries[0].CustomType != "kept" || result.Continue {
				t.Fatalf("boundary mutation lost across SDK: %+v", result)
			}
			wantErrors := 0
			if outcome != "return" {
				wantErrors = 1
			}
			if len(handlerErrors) != wantErrors {
				t.Fatalf("handler errors = %+v, want %d", handlerErrors, wantErrors)
			}
		})
	}
}
