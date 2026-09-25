//go:build windows

package coding

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func fauxPowerShellCall(id, command string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{
			Content:  []ai.AssistantContentBlock{ai.ToolCall{ID: id, Name: "powershell", Arguments: ai.JsonObject{"command": command}}},
			Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(),
		}
	}
}

// On Windows the powershell tool's calls reach extensions with upstream's
// PowerShellToolCallEvent and PowerShellToolResultEvent shapes: the wire JSON
// decodes to those variants, a handler can block a call before PowerShell
// runs, and an allowed call's real output reaches tool_result.
func TestPowerShellToolEventsReachExtensionsOnWindows(t *testing.T) {
	var mu sync.Mutex
	var calls, results [][]byte
	record := func(into *[][]byte, event any) {
		data, err := json.Marshal(event)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		*into = append(*into, data)
	}
	guard := extension.Extension{Path: "/ext/powershell-guard", Handlers: map[string][]extension.HandlerFn{
		"tool_call": {func(args ...any) (any, error) {
			record(&calls, args[0])
			if event, ok := args[0].(extension.CustomToolCallEvent); ok && event.Input["command"] == "Remove-Item blocked.txt" {
				return &extension.ToolCallEventResult{Block: true, Reason: "blocked by guard"}, nil
			}
			return nil, nil
		}},
		"tool_result": {func(args ...any) (any, error) {
			record(&results, args[0])
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{
		tools:     []agent.AgentTool{&tools.PowerShellTool{CWD: t.TempDir()}},
		extension: guard,
	},
		fauxPowerShellCall("ps-allowed", "Write-Output ('pig' + '-' + 'powershell')"),
		fauxPowerShellCall("ps-blocked", "Remove-Item blocked.txt"),
		fauxReply("done", ai.StopReasonStop, 0))
	messages, err := h.session.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("tool_call events = %d, want 2", len(calls))
	}
	for i, command := range []string{"Write-Output ('pig' + '-' + 'powershell')", "Remove-Item blocked.txt"} {
		event, err := extension.UnmarshalToolCallEvent(calls[i])
		if err != nil {
			t.Fatal(err)
		}
		ps, ok := event.(extension.PowerShellToolCallEvent)
		input, _ := ps.Input.(map[string]any)
		if !ok || ps.Type != "tool_call" || input["command"] != command {
			t.Fatalf("tool_call %d wire = %s, want a PowerShellToolCallEvent for %q", i, calls[i], command)
		}
	}
	// Upstream agent-loop finalizes a blocked call immediately, without
	// afterToolCall, so only the executed call emits tool_result.
	if len(results) != 1 {
		t.Fatalf("tool_result events = %d, want 1 (the blocked call emits none)", len(results))
	}
	event, err := extension.UnmarshalToolResultEvent(results[0])
	if err != nil {
		t.Fatal(err)
	}
	allowed, ok := event.(extension.PowerShellToolResultEvent)
	if !ok || allowed.IsError || !strings.Contains(string(results[0]), "pig-powershell") {
		t.Fatalf("allowed tool_result wire = %s, want a PowerShellToolResultEvent with PowerShell's output", results[0])
	}
	toolResults := toolResultMessages(messages)
	if len(toolResults) != 2 || !toolResults[1].IsError || !strings.Contains(toolResults[1].Text(), "blocked by guard") {
		t.Fatalf("tool results = %+v, want the blocked call to report the guard's reason", toolResults)
	}
}
