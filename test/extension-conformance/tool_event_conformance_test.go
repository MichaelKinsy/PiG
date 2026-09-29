package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream 0.87.1 types.ts gives the powershell tool its own
// PowerShellToolCallEvent and PowerShellToolResultEvent variants, with the
// bash input and details shapes. Every SDK must receive both variants with
// that shape, block a call, and replace a result exactly as the in-process
// reference does. The recorded values are distinct from every SDK's fallback,
// so an SDK that never runs its handler cannot match.
func TestToolEventVariantsMatchAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := allHarnessCases()
	var want []string
	for _, tool := range []string{"powershell", "bash"} {
		want = append(want,
			"tool-call="+tool+":Get-Date:5:info",
			"tool-call="+tool+":blocked-command:7:info",
			"tool-result="+tool+`:C:\pig\tool-output.log:9:output:info`,
		)
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()

			var results []string
			for _, tool := range []string{"powershell", "bash"} {
				allowed, err := h.runner.EmitToolCall(ctx, conformanceToolCall(tool, "Get-Date", 5))
				if err != nil {
					t.Fatalf("%s tool_call: %v", tool, err)
				}
				blocked, err := h.runner.EmitToolCall(ctx, conformanceToolCall(tool, "blocked-command", 7))
				if err != nil {
					t.Fatalf("%s blocked tool_call: %v", tool, err)
				}
				replaced, err := h.runner.EmitToolResult(ctx, conformanceToolResult(tool))
				if err != nil {
					t.Fatalf("%s tool_result: %v", tool, err)
				}
				results = append(results, fmt.Sprintf("%s:allowed=%t blocked=%t:%s replaced=%v", tool,
					allowed == nil || !allowed.Block, blocked != nil && blocked.Block, blockReason(blocked), replacedText(replaced)))
			}
			wantResults := []string{
				"powershell:allowed=true blocked=true:blocked powershell replaced=powershell redacted",
				"bash:allowed=true blocked=true:blocked bash replaced=bash redacted",
			}
			if !reflect.DeepEqual(results, wantResults) {
				t.Fatalf("handler results = %#v, want %#v", results, wantResults)
			}
			toolEvents := func() []string {
				var recorded []string
				for _, event := range h.ui.Recorded() {
					if strings.HasPrefix(event, "tool-call=") || strings.HasPrefix(event, "tool-result=") {
						recorded = append(recorded, event)
					}
				}
				return recorded
			}
			waitFor(t, func() bool { return len(toolEvents()) >= len(want) })
			if got := toolEvents(); !reflect.DeepEqual(got, want) {
				t.Fatalf("recorded tool events = %#v, want %#v", got, want)
			}
		})
	}
}

func conformanceToolCall(tool, command string, timeout float64) extension.ToolCallEvent {
	base := extension.ToolCallEventBase{Type: "tool_call", ToolCallID: tool + "-" + command}
	input := map[string]any{"command": command, "timeout": timeout}
	if tool == "powershell" {
		return extension.PowerShellToolCallEvent{ToolCallEventBase: base, ToolName: tool, Input: input}
	}
	return extension.BashToolCallEvent{ToolCallEventBase: base, ToolName: tool, Input: input}
}

func conformanceToolResult(tool string) extension.ToolResultEvent {
	base := extension.ToolResultEventBase{
		Type: "tool_result", ToolCallID: tool + "-result", Input: map[string]any{"command": "Get-Date"},
		Content: []any{map[string]any{"type": "text", "text": "output"}},
	}
	details := &extension.BashToolDetails{
		FullOutputPath: `C:\pig\tool-output.log`,
		Truncation: &extension.ToolTruncation{
			Content: "output", Truncated: true, TruncatedBy: "lines", TotalLines: 9, TotalBytes: 90,
			OutputLines: 1, OutputBytes: 6, MaxLines: 1, MaxBytes: 50000,
		},
	}
	if tool == "powershell" {
		return extension.PowerShellToolResultEvent{ToolResultEventBase: base, ToolName: tool, Details: details}
	}
	return extension.BashToolResultEvent{ToolResultEventBase: base, ToolName: tool, Details: details}
}

func blockReason(result *extension.ToolCallEventResult) string {
	if result == nil {
		return ""
	}
	return result.Reason
}

func replacedText(result *extension.ToolResultEventResult) any {
	if result == nil || len(result.Content) == 0 {
		return nil
	}
	return asMap(result.Content[0])["text"]
}

// asMap reads a JSON-shaped value as an object: a map as is, or a struct or
// raw JSON through its JSON encoding.
func asMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	var m map[string]any
	if data, err := json.Marshal(value); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}
