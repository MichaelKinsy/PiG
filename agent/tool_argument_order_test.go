package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A tool receives the arguments in the order the model wrote them. Pi validates structuredClone of the parsed arguments and
// returns it (validation.ts:317-339), so a tool that logs JSON.stringify(params), an MCP server and the session file all see
// the model's member order. Coercion changes a value, never a member's position.
// upstream: .upstream/v0.99.1/packages/ai/src/utils/validation.ts:317-339; agent/src/agent-loop.ts:707-716 (prepareToolCall).
const orderedToolArguments = `{"zeta":"5","alpha":"a","nested":{"yy":1,"bb":[{"qq":1,"aa":2}]}}`

var orderedToolSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"alpha":  map[string]any{"type": "string"},
		"nested": map[string]any{"type": "object"},
		"zeta":   map[string]any{"type": "number"},
	},
	"required": []any{"zeta"},
}

func TestValidateToolArgsKeepsMemberOrder(t *testing.T) {
	const want = `{"zeta":5,"alpha":"a","nested":{"yy":1,"bb":[{"qq":1,"aa":2}]}}`
	got, err := validateToolArgs("probe", orderedToolSchema, json.RawMessage(orderedToolArguments))
	if err != nil || string(got) != want {
		t.Fatalf("validateToolArgs = %s, %v; want %s", got, err, want)
	}
	rawSchema, _ := json.Marshal(orderedToolSchema)
	got, err = validateToolArgsSchema("probe", rawSchema, json.RawMessage(orderedToolArguments))
	if err != nil || string(got) != want {
		t.Fatalf("validateToolArgsSchema = %s, %v; want %s", got, err, want)
	}
	// Arguments that need no coercion come back as written.
	got, err = validateToolArgs("probe", map[string]any{}, json.RawMessage(`{"z":1,"10":2,"a":3}`))
	if err != nil || string(got) != `{"10":2,"z":1,"a":3}` {
		t.Fatalf("no schema = %s, %v; want integer-like keys first, then insertion order", got, err)
	}
}

// pendingToolCall.args is the text hooks, events and the tool receive, so it is the call's arguments in the model's order.
func TestPendingToolCallKeepsArgumentOrder(t *testing.T) {
	var call ai.ToolCall
	call.ID, call.Name = "c1", "probe"
	call.SetStreamingArguments(orderedToolArguments)
	pending := pendingToolCalls(&AssistantMessage{Content: []ai.AssistantContentBlock{call}})
	if len(pending) != 1 {
		t.Fatalf("pending calls = %d, want 1", len(pending))
	}
	if got := pending[0].args.String(); got != orderedToolArguments {
		t.Fatalf("pending arguments = %s, want %s", got, orderedToolArguments)
	}
}

// The session file is the JSON of the message. A persisted assistant turn keeps the arguments' order through the decode that
// resumes the session and the encode that writes it again.
func TestAgentMessageToolCallArgumentsKeepWrittenMemberOrder(t *testing.T) {
	const wire = `{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"probe","arguments":` + orderedToolArguments + `}],"timestamp":1}`
	var message AgentMessage
	if err := json.Unmarshal([]byte(wire), &message); err != nil {
		t.Fatal(err)
	}
	for name, subject := range map[string]AgentMessage{"decoded": message, "clone": message.Clone()} {
		encoded, err := json.Marshal(subject)
		if err != nil {
			t.Fatal(err)
		}
		if got := persistedToolCallArguments(t, encoded); got != orderedToolArguments {
			t.Errorf("%s message wrote arguments %s, want %s", name, got, orderedToolArguments)
		}
	}
}

// persistedToolCallArguments returns the text of the first tool call's arguments in an encoded assistant message, or "" when it has none.
func persistedToolCallArguments(t *testing.T, encoded []byte) string {
	t.Helper()
	var decoded struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	var blocks []struct {
		Type      string          `json:"type"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(decoded.Content, &blocks) != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type == "toolCall" {
			return string(block.Arguments)
		}
	}
	return ""
}

type argumentRecordingTool struct {
	mu  sync.Mutex
	got []string
}

func (*argumentRecordingTool) Name() string        { return "probe" }
func (*argumentRecordingTool) Label() string       { return "probe" }
func (*argumentRecordingTool) Description() string { return "probe" }
func (*argumentRecordingTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "probe", Parameters: orderedToolSchema}
}
func (*argumentRecordingTool) ExecutionMode() ToolExecutionMode { return ToolModeSequential }
func (tool *argumentRecordingTool) Execute(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	tool.got = append(tool.got, string(args))
	return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
}

// The production loop: a model's tool call reaches the before hook and the tool, and lands in the transcript, in the model's
// order.
func TestAgentLoopDeliversToolArgumentsInModelOrder(t *testing.T) {
	var call ai.ToolCall
	call.ID, call.Name = "c1", "probe"
	call.SetStreamingArguments(orderedToolArguments)
	start := agentTestAssistant(nil, ai.StopReasonPending)
	partial := agentTestAssistant([]ai.AssistantContentBlock{call}, ai.StopReasonPending)
	events := []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: start},
		ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial},
		ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call, Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: agentTestAssistant([]ai.AssistantContentBlock{call}, ai.StopReasonToolUse)},
	}
	tool := &argumentRecordingTool{}
	agent := NewAgent(AgentOptions{Model: fakeTestModel(providerFromSeqs(events, textSeq("done"))), Tools: []AgentTool{tool}, MaxTurns: 5})
	var hooked string
	agent.AddBeforeToolCallHook(func(_ context.Context, _, _ string, args json.RawMessage) ToolCallHookResult {
		hooked = string(args)
		return ToolCallHookResult{}
	})
	messages, err := agent.Send(t.Context(), "go")
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"zeta":5,"alpha":"a","nested":{"yy":1,"bb":[{"qq":1,"aa":2}]}}`
	if len(tool.got) != 1 || tool.got[0] != want {
		t.Errorf("tool received %q, want [%s]", tool.got, want)
	}
	if hooked != want {
		t.Errorf("before hook saw %s, want %s", hooked, want)
	}
	persisted := 0
	for _, message := range messages {
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if got := persistedToolCallArguments(t, encoded); got != "" {
			persisted++
			if got != orderedToolArguments {
				t.Errorf("persisted arguments %s, want %s", got, orderedToolArguments)
			}
		}
	}
	if persisted != 1 {
		t.Errorf("%d persisted tool calls, want 1", persisted)
	}
}
