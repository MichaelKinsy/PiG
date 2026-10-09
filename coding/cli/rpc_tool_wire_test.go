package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi 0.87.1 coding-agent/src/core/tools/bash.ts:301-302 emits content:[] before execution.
// Later output updates use text blocks (bash.ts:270-276); read.ts:185-188 retains an explicit empty text result.
func TestRPCShellInitialUpdateHasEmptyContent(t *testing.T) {
	for _, command := range []string{"true", "printf hello", `printf '\357\273\277'`} {
		t.Run(command, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"command": command})
			if err != nil {
				t.Fatal(err)
			}
			var updates []any
			result, err := (&tools.BashTool{CWD: t.TempDir()}).Execute(t.Context(), "call", args, func(partial agent.AgentToolResult) {
				events, err := rpcAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "call", ToolName: "bash", PartialResult: partial, Args: args})
				if err != nil {
					t.Error(err)
					return
				}
				updates = append(updates, decodeRPCEvent(t, events[0]))
			})
			if err != nil || result.IsError {
				t.Fatalf("execute: %+v, %v", result, err)
			}
			want := map[string]any{
				"type": "tool_execution_update", "toolCallId": "call", "toolName": "bash",
				"args": map[string]any{"command": command}, "partialResult": map[string]any{"content": []any{}},
			}
			if len(updates) == 0 || !reflect.DeepEqual(updates[0], want) {
				t.Fatalf("initial update = %#v, want %#v", updates, want)
			}
			if command != "true" {
				text := "hello"
				if command != "printf hello" {
					text = "" // A BOM-only chunk decodes to an explicit empty output update, not the initial snapshot.
				}
				want["partialResult"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "details": map[string]any{}}
				if len(updates) < 2 || !reflect.DeepEqual(updates[len(updates)-1], want) {
					t.Fatalf("output update = %#v, want %#v", updates, want)
				}
			}
		})
	}
	// An empty completed read is text, not an empty partial snapshot.
	events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "read", ToolName: "read", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"content": []any{map[string]any{"type": "text", "text": ""}}}
	if got := decodeRPCEvent(t, events[0])["result"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty read result = %#v, want %#v", got, want)
	}
}

// Pi 0.99.1 packages/agent/src/agent-loop.ts:912-919 emits `result: finalized.result, isError: finalized.isError`.
// A thrown error, an unknown tool, invalid arguments and a blocked call give createErrorToolResult, `{content, details: {}}` with no isError
// (agent-loop.ts:906-910), so the outer flag is the only one on the wire; a tool that RETURNS isError keeps it inside result
// (types.ts:436-441, bash.ts:403-409). Pi 0.87.1 tools only threw (agent-loop.ts:870-894), so its result never carried isError.
// The persisted toolResult message carries the outer flag (agent-loop.ts:930).
func TestRPCToolExecutionResultExactWire(t *testing.T) {
	for _, failed := range []bool{false, true} {
		events, err := rpcAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call", ToolName: "read", IsError: failed, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result"}}}})
		if err != nil {
			t.Fatal(err)
		}
		got := decodeRPCEvent(t, events[0])
		want := map[string]any{"type": "tool_execution_end", "toolCallId": "call", "toolName": "read", "isError": failed, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "result"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
		message, err := rpcToolResultMessage(agent.ToolResultMessage{ToolCallID: "call", ToolName: "read", IsError: failed})
		if err != nil {
			t.Fatal(err)
		}
		if decodeRPCEvent(t, message)["isError"] != failed {
			t.Fatal("persisted toolResult lost isError")
		}
	}
	events, err := rpcAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "call", ToolName: "bash", PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}}, Args: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	partial := decodeRPCEvent(t, events[0])["partialResult"]
	want := map[string]any{"content": []any{map[string]any{"type": "text", "text": "partial"}}}
	if !reflect.DeepEqual(partial, want) {
		t.Fatalf("partialResult = %#v, want %#v", partial, want)
	}
}

// Pi 0.99.1 writes the finalized AgentToolResult as the tool built it (agent-loop.ts:912-919), so `isError`, `usage` and `terminate` sit inside `result`
// when the tool or a hook set them, after content, details and structuredContent (bash.ts:403-409 builds content, details, structuredContent, isError).
// afterToolCall's result is spread over the tool's (agent-loop.ts:877-889): keys keep their positions, and a structuredContent the hook adds to a result that had none comes last.
// Pi 0.99.1 with a tool_result handler returning details for the same bash call writes content, details, structuredContent, isError.
// The bash case is Pi 0.99.1's own output (dist/cli.js, --mode json, test-faux twin, `echo out; exit 3`).
func TestRPCToolExecutionEndWritesResultIsErrorUsageAndTerminateInPiOrder(t *testing.T) {
	text := []ai.ToolResultMessageContent{ai.TextContent{Text: "out\n\n\nCommand exited with code 3"}}
	structured := json.RawMessage(`{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0}`)
	usage := &ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10, Cost: ai.UsageCost{Input: 0.5, Total: 0.5}}
	const usageWire = `{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0.5,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.5}}`
	const head = `{"type":"tool_execution_end","toolCallId":"call","toolName":"bash","result":`
	tests := []struct {
		name  string
		event agent.ToolExecutionEndEvent
		want  string
	}{
		{
			name:  "returned failure keeps isError inside result",
			event: agent.ToolExecutionEndEvent{IsError: true, Result: agent.AgentToolResult{Content: text, StructuredContent: structured, IsError: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0},"isError":true},"isError":true}`,
		},
		{
			name:  "returned failure keeps details before structuredContent",
			event: agent.ToolExecutionEndEvent{IsError: true, Result: agent.AgentToolResult{Content: text, Details: map[string]any{"d": 1}, StructuredContent: structured, IsError: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"details":{"d":1},"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0},"isError":true},"isError":true}`,
		},
		{
			name:  "thrown error has no result.isError",
			event: agent.ToolExecutionEndEvent{IsError: true, Result: agent.AgentToolResult{Content: text, Details: map[string]any{}}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"details":{}},"isError":true}`,
		},
		{
			name:  "hook cleared isError of a returned failure",
			event: agent.ToolExecutionEndEvent{Result: agent.AgentToolResult{Content: text, IsError: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"isError":true},"isError":false}`,
		},
		{
			name:  "blocked call terminates without isError",
			event: agent.ToolExecutionEndEvent{IsError: true, Result: agent.AgentToolResult{Content: text, Details: map[string]any{}, Terminate: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"details":{},"terminate":true},"isError":true}`,
		},
		{
			name:  "tool usage and terminate follow isError",
			event: agent.ToolExecutionEndEvent{Result: agent.AgentToolResult{Content: text, Details: map[string]any{}, StructuredContent: structured, Usage: usage, Terminate: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"details":{},"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0},"usage":` + usageWire + `,"terminate":true},"isError":false}`,
		},
		{
			name:  "a hook that kept structuredContent leaves it in place",
			event: agent.ToolExecutionEndEvent{IsError: true, Result: agent.AgentToolResult{Content: text, Details: map[string]any{"note": "kept"}, StructuredContent: structured, IsError: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"details":{"note":"kept"},"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0},"isError":true},"isError":true}`,
		},
		{
			name:  "a hook that added structuredContent puts it last",
			event: agent.ToolExecutionEndEvent{IsError: true, Result: agent.AgentToolResult{Content: text, Details: map[string]any{"d": 1}, StructuredContent: structured, IsError: true, StructuredContentAppended: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"details":{"d":1},"isError":true,"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0}},"isError":true}`,
		},
		{
			name:  "a hook that added structuredContent keeps usage and terminate before it",
			event: agent.ToolExecutionEndEvent{Result: agent.AgentToolResult{Content: text, Details: map[string]any{}, StructuredContent: structured, Usage: usage, Terminate: true, StructuredContentAppended: true}},
			want:  head + `{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"details":{},"usage":` + usageWire + `,"terminate":true,"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0}},"isError":false}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.event.ToolCallID, tc.event.ToolName = "call", "bash"
			events, err := rpcAgentEvent(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(events[0])
			if err != nil {
				t.Fatal(err)
			}
			if string(wire) != tc.want {
				t.Fatalf("wire = %s\nwant   %s", wire, tc.want)
			}
		})
	}
}

// The real `pig --mode rpc` binary against the test-faux twin of the Pi 0.99.1 probe: a bash call that exits 3 returns an error result, so
// tool_execution_end.result carries isError (bash.ts:403-409, agent-loop.ts:912-919); a read of a missing file throws, so its result is
// createErrorToolResult without isError (read.ts, agent-loop.ts:906-910). Both events carry the call's flag.
func TestRPCProcessToolExecutionEndKeepsReturnedIsErrorApartFromThrown(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--no-session", "--provider", "test-faux", "--model", "faux-1")
	for _, tc := range []struct {
		prompt, tool  string
		resultIsError bool
	}{
		{"Run: bash failing exit", "bash", true},
		{"Run: read parity-read-target.txt", "read", false},
	} {
		p.send(`{"id":"p","type":"prompt","message":"` + tc.prompt + `"}`)
		var end rpcRecord
		p.await(tc.tool+" tool_execution_end", func(record rpcRecord) bool {
			if record["type"] == "tool_execution_end" {
				end = record
			}
			return record["type"] == "agent_settled"
		})
		if end == nil || end["toolName"] != tc.tool || end["isError"] != true {
			t.Fatalf("%s: tool_execution_end = %#v, want the call's isError true", tc.tool, end)
		}
		result, _ := end["result"].(map[string]any)
		if got, present := result["isError"]; present != tc.resultIsError || (present && got != true) {
			t.Fatalf("%s: result = %#v, want isError present=%v", tc.tool, result, tc.resultIsError)
		}
		if tc.resultIsError {
			structured, _ := result["structuredContent"].(map[string]any)
			if structured["exit_code"] != float64(3) || structured["output"] != "out\n" {
				t.Fatalf("%s: structuredContent = %#v", tc.tool, structured)
			}
		} else if result["details"] == nil {
			t.Fatalf("%s: a thrown result keeps its empty details: %#v", tc.tool, result)
		}
	}
}

// A tool's result is written on the JSON and RPC streams as the tool built it: its members in its own order (agent-loop.ts:778-786, 912-919). The expectation is what real Pi 0.99.1 printed for a tool returning {details, isError, content} (and, with a tool_result handler that returned {details}, {isError, content, details}).
func TestRPCToolResultWritesMembersInTheToolsOrder(t *testing.T) {
	text := []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}
	for _, tc := range []struct {
		name  string
		event agent.AgentEvent
		want  string
	}{
		{
			name:  "end",
			event: agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "t", IsError: true, Result: agent.AgentToolResult{MemberOrder: []string{"details", "isError", "content"}, Content: text, Details: json.RawMessage(`{"zeta":1,"alpha":2}`), IsError: true}},
			want:  `{"type":"tool_execution_end","toolCallId":"c","toolName":"t","result":{"details":{"zeta":1,"alpha":2},"isError":true,"content":[{"type":"text","text":"x"}]},"isError":true}`,
		},
		{
			name:  "update",
			event: agent.ToolExecutionUpdateEvent{ToolCallID: "c", ToolName: "t", Args: json.RawMessage(`{}`), PartialResult: agent.AgentToolResult{MemberOrder: []string{"details", "content"}, Content: text, Details: json.RawMessage(`{"zeta":1}`)}},
			want:  `{"type":"tool_execution_update","toolCallId":"c","toolName":"t","args":{},"partialResult":{"details":{"zeta":1},"content":[{"type":"text","text":"x"}]}}`,
		},
		{
			name:  "a hook added details after the tool's members",
			event: agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "t", IsError: true, Result: agent.AgentToolResult{MemberOrder: []string{"isError", "content"}, Content: text, Details: "added", IsError: true}},
			want:  `{"type":"tool_execution_end","toolCallId":"c","toolName":"t","result":{"isError":true,"content":[{"type":"text","text":"x"}],"details":"added"},"isError":true}`,
		},
		{
			// Pi 0.99.1 wrote {"content":[...],"isError":false,"details":null,"terminate":false} for a tool returning those members.
			name:  "members the tool wrote as null or false",
			event: agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "t", Result: agent.AgentToolResult{MemberOrder: []string{"content", "isError", "details", "terminate", "usage"}, Content: text}},
			want:  `{"type":"tool_execution_end","toolCallId":"c","toolName":"t","result":{"content":[{"type":"text","text":"x"}],"isError":false,"details":null,"terminate":false,"usage":null},"isError":false}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := rpcAgentEvent(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(events[0])
			if err != nil {
				t.Fatal(err)
			}
			if string(wire) != tc.want {
				t.Fatalf("wire = %s\nwant   %s", wire, tc.want)
			}
		})
	}
}
