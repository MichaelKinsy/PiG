package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestConformance_ToolResultGuardsAgreeAcrossSDKs: Pi's isBashToolResult ... isLsToolResult (extensions/types.ts:1314-1338) are `e.toolName === "<tool>"`.
// The production Runner dispatches the CustomToolResultEvent the Session emits for every tool; the handler each SDK registers reports which of its eight guards accept
// the event it received. Every SDK accepts exactly the guard of the event's tool and none for a custom tool, isolated and packed. The in-process Go
// reference (extension.Is<Tool>ToolResult on the event the Runner hands its handler) states the same row.
func TestConformance_ToolResultGuardsAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	tools := []string{"bash", "powershell", "read", "edit", "write", "grep", "find", "ls", "probe"}
	emit := func(t *testing.T, runner *inproc.Runner, tool string) {
		t.Helper()
		if _, err := runner.EmitToolResult(context.Background(), extension.CustomToolResultEvent{
			ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "c1", Input: map[string]any{}, Content: []any{}},
			ToolName:            tool,
		}); err != nil {
			t.Fatalf("emit %s: %v", tool, err)
		}
	}
	t.Run("inproc-go", func(t *testing.T) {
		guards := map[string]func(extension.ToolResultEvent) bool{
			"bash": extension.IsBashToolResult, "powershell": extension.IsPowerShellToolResult, "read": extension.IsReadToolResult, "edit": extension.IsEditToolResult,
			"write": extension.IsWriteToolResult, "grep": extension.IsGrepToolResult, "find": extension.IsFindToolResult, "ls": extension.IsLsToolResult,
		}
		var reports []string
		runner := inproc.NewRunner([]extension.Extension{{
			Path: "inproc-tool-result-guards", ResolvedPath: "inproc-tool-result-guards",
			Handlers: map[string][]extension.HandlerFn{"tool_result": {func(args ...any) (any, error) {
				event, _ := args[0].(extension.ToolResultEvent)
				var matched []string
				for name, guard := range guards {
					if guard(event) {
						matched = append(matched, name)
					}
				}
				slices.Sort(matched)
				reports = append(reports, strings.Join(matched, ","))
				return nil, nil
			}}},
		}}, t.TempDir())
		for i, tool := range tools {
			emit(t, runner, tool)
			want := tool
			if tool == "probe" {
				want = ""
			}
			if len(reports) != i+1 || reports[i] != want {
				t.Fatalf("guards accepting a %s result: reports %q, want %q last", tool, reports, want)
			}
		}
	})
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			*h.notify = (*h.notify)[:0]
			runConformanceCommandArgs(t, h, "tool_result_guards_probe", "")
			for i, tool := range tools {
				want := tool
				if tool == "probe" {
					want = ""
				}
				emit(t, h.runner, tool)
				var reports []string
				pollUntilConformance(t, tool+": the handler never reported its guards", func() bool {
					reports = reports[:0]
					for _, n := range *h.notify {
						if rest, ok := strings.CutPrefix(n, "tool_result_guards:"); ok {
							reports = append(reports, strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end"))
						}
					}
					return len(reports) > i
				})
				if reports[i] != want {
					t.Fatalf("guards accepting a %s result = %q, want %q", tool, reports[i], want)
				}
			}
		})
	}
}
