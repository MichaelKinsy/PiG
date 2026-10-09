package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// wireToolFlowError checks the tool-flow rule Anthropic Messages (and the other
// providers) enforce on the converted wire: a tool result block must directly
// follow the assistant message that holds its tool call, every tool call is
// answered exactly once, and nothing else sits between the call and its
// results. It returns "" for a valid transcript.
func wireToolFlowError(wire []ai.Message) string {
	for i := 0; i < len(wire); i++ {
		switch message := wire[i].(type) {
		case ai.AssistantMessage:
			pending := map[string]bool{}
			for _, block := range message.Content {
				if call, ok := block.(ai.ToolCall); ok {
					pending[call.ID] = true
				}
			}
			j := i + 1
			for ; j < len(wire); j++ {
				result, ok := wire[j].(ai.ToolResultMessage)
				if !ok {
					break
				}
				if !pending[result.ToolCallID] {
					return fmt.Sprintf("wire[%d]: tool result %q has no tool call in the previous assistant message", j, result.ToolCallID)
				}
				delete(pending, result.ToolCallID)
			}
			for id := range pending {
				return fmt.Sprintf("wire[%d]: tool call %q is not answered directly after its assistant message", i, id)
			}
			i = j - 1
		case ai.ToolResultMessage:
			return fmt.Sprintf("wire[%d]: tool result %q does not follow an assistant message", i, message.ToolCallID)
		}
	}
	return ""
}

func boundaryParallelCalls() AgentMessage {
	return AgentMessage{Assistant: &AssistantMessage{
		Role: "assistant",
		Content: []ai.AssistantContentBlock{
			ai.ToolCall{ID: "call_a", Name: "read", Arguments: ai.JsonObject{"path": "a"}},
			ai.ToolCall{ID: "call_b", Name: "read", Arguments: ai.JsonObject{"path": "b"}},
		},
		StopReason: "toolUse",
	}}
}

func boundaryToolResult(id string) AgentMessage {
	return AgentMessage{ToolResult: &ToolResultMessage{
		Role: RoleToolResult, ToolCallID: id, ToolName: "read",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok " + id}},
	}}
}

func boundaryUserPrompt(text string) AgentMessage {
	return AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
}

// A custom message that convertToLLM turns into a user message is a user turn
// for tool-flow accounting. Upstream converts before transformMessages, so a
// branch summary, compaction summary, bash execution or custom message that
// lands between a tool call and its remaining results interrupts the tool flow
// and the missing result is synthesized before it. Pig normalizes before the
// conversion; treating those messages as non-interrupting left the synthetic
// result after the user message, an orphan tool_result Anthropic rejects with
// "unexpected tool_use_id in tool_result blocks".
func TestConvertToLLMCustomUserMessageInterruptsToolFlow(t *testing.T) {
	customs := map[string]map[string]any{
		RoleBranchSummary:     {"role": RoleBranchSummary, "summary": "went elsewhere", "fromId": "x", "timestamp": int64(5)},
		RoleCompactionSummary: {"role": RoleCompactionSummary, "summary": "earlier", "tokensBefore": 1, "timestamp": int64(5)},
		RoleBashExecution:     {"role": RoleBashExecution, "command": "ls", "output": "x", "exitCode": 0, "timestamp": int64(5)},
		RoleCustom:            {"role": RoleCustom, "customType": "note", "content": "hello", "display": true, "timestamp": int64(5)},
	}
	for name, custom := range customs {
		t.Run(name, func(t *testing.T) {
			history := []AgentMessage{
				boundaryUserPrompt("start"),
				boundaryParallelCalls(),
				boundaryToolResult("call_a"),
				{Custom: custom},
				boundaryUserPrompt("prepare for compaction"),
			}
			wire := convertToLLM(history, nil)
			if problem := wireToolFlowError(wire); problem != "" {
				t.Fatalf("%s\nwire: %v", problem, wire)
			}
		})
	}
}

// A custom message that converts to nothing (an excluded bash execution) sends
// no wire message, so it must not close a pending tool call early.
func TestConvertToLLMCustomMessageWithoutWireOutputKeepsToolFlow(t *testing.T) {
	history := []AgentMessage{
		boundaryUserPrompt("start"),
		boundaryParallelCalls(),
		boundaryToolResult("call_a"),
		{Custom: map[string]any{"role": RoleBashExecution, "command": "ls", "output": "x", "excludeFromContext": true}},
		boundaryToolResult("call_b"),
		boundaryUserPrompt("next"),
	}
	wire := convertToLLM(history, nil)
	if problem := wireToolFlowError(wire); problem != "" {
		t.Fatalf("%s\nwire: %v", problem, wire)
	}
	results := 0
	for _, message := range wire {
		if result, ok := message.(ai.ToolResultMessage); ok {
			results++
			if result.IsError {
				t.Fatalf("real result for %q replaced by a synthetic one", result.ToolCallID)
			}
		}
	}
	if results != 2 {
		t.Fatalf("got %d tool results, want 2", results)
	}
}

// A system message between a tool call and its results (a refreshed system
// prompt or a tool change after /tree navigation) is held back and emitted after
// the results, synthetic ones included (upstream transformMessages
// heldSystemMessages). Left in place it separates the call from its results.
func TestConvertToLLMHoldsSystemMessageUntilToolFlowCloses(t *testing.T) {
	system := AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("updated"), Timestamp: 9}}
	history := []AgentMessage{
		boundaryUserPrompt("start"),
		boundaryParallelCalls(),
		boundaryToolResult("call_a"),
		system,
		boundaryUserPrompt("prepare for compaction"),
	}
	wire := convertToLLM(history, nil)
	if problem := wireToolFlowError(wire); problem != "" {
		t.Fatalf("%s\nwire: %v", problem, wire)
	}
	// user, assistant, result a, synthetic result b, system, user.
	if _, ok := wire[4].(ai.SystemMessage); !ok || len(wire) != 6 {
		t.Fatalf("system message not emitted after the closed tool flow: %v", wire)
	}
}

// The same histories reach Anthropic Messages as a tool_result block whose
// tool_use is not in the previous message unless the tool flow closes at the
// custom user message. Drives the real provider conversion and reads the
// serialized request.
func TestAnthropicRequestAfterBranchSummaryHasNoOrphanToolResult(t *testing.T) {
	history := []AgentMessage{
		boundaryUserPrompt("start"),
		boundaryParallelCalls(),
		boundaryToolResult("call_a"),
		{Custom: map[string]any{"role": RoleBranchSummary, "summary": "went elsewhere", "fromId": "x", "timestamp": int64(5)}},
		boundaryUserPrompt("prepare for compaction"),
	}
	var anthropic func(string) ai.Provider
	for _, p := range providerBuilders() {
		if p.name == "anthropic-messages" {
			anthropic = p.make
		}
	}
	body := captureStreamWire(t, anthropicSSE, anthropic, convertToLLM(history, nil))
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content json.RawMessage
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	var previousToolUses map[string]bool
	for index, message := range request.Messages {
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(message.Content, &blocks) != nil {
			previousToolUses = nil
			continue
		}
		uses := map[string]bool{}
		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				uses[block.ID] = true
			case "tool_result":
				if !previousToolUses[block.ToolUseID] {
					t.Fatalf("messages.%d: unexpected tool_use_id %q in tool_result blocks: %s", index, block.ToolUseID, body)
				}
			}
		}
		previousToolUses = uses
	}
}
