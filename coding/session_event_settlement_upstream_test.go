package coding

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// createEchoTool preserves the upstream helper's default parallel execution and text/details result.
func createEchoTool(t *testing.T) *bridgeTool {
	t.Helper()
	tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{
		Name: "echo", Label: "Echo", Description: "Echo text back",
		Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Execute: func(_ context.Context, _ string, args json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			var params struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return extension.AgentToolResult{}, err
			}
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: params.Text}}, Details: map[string]any{"text": params.Text}}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func persistedBranchRoles(session *Session) []string {
	roles := []string{}
	for _, entry := range session.inner.GetBranch() {
		if message, ok := entry.(icodingagent.MessageEntry); ok {
			roles = append(roles, message.Message.Role())
		}
	}
	return roles
}

func echoCalls(texts ...string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		content := make([]ai.AssistantContentBlock, len(texts))
		for i, text := range texts {
			content[i] = ai.ToolCall{ID: "call-" + text, Name: "echo", Arguments: ai.JsonObject{"text": text}}
		}
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Content: content}
	}
}

// upstream: packages/coding-agent/test/suite/regressions/1717-2113-agent-session-event-settlement.test.ts:29
func TestUpstreamEventSettlementKeepsPersistedAssistantToolResultOrderWhenMessageEndHandlersYield(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{createEchoTool(t)}, extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"message_end": {func(args ...any) (any, error) {
				event := args[0].(extension.MessageEndEvent)
				message := event.Message
				if message.Assistant != nil {
					// The original handler awaits a 20ms timer. Synctest advances it virtually; it is not a synchronization sleep.
					<-time.After(20 * time.Millisecond)
				}
				return nil, nil
			}},
		}}}, echoCalls("one", "two"), fauxReply("done", ai.StopReasonStop, 0))
		if _, err := h.session.Send(t.Context(), "run tools"); err != nil {
			t.Fatal(err)
		}
		roles := persistedBranchRoles(h.session)
		want := []string{"system", "user", "assistant", "toolResult", "toolResult", "assistant"}
		if !slices.Equal(roles, want) {
			t.Fatalf("persisted roles=%v want=%v", roles, want)
		}
		firstToolResult := slices.Index(roles, "toolResult")
		if firstToolResult <= 0 || roles[firstToolResult-1] != "assistant" {
			t.Fatalf("first tool result at %d in %v", firstToolResult, roles)
		}
	})
}

// upstream: packages/coding-agent/test/suite/regressions/1717-2113-agent-session-event-settlement.test.ts:68
func TestUpstreamEventSettlementRunsToolCallAfterAssistantIsSettled(t *testing.T) {
	var h *recoveryHarness
	var seen [][]string
	h = newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{createEchoTool(t)}, extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"tool_call": {func(...any) (any, error) { seen = append(seen, persistedBranchRoles(h.session)); return nil, nil }},
	}}}, echoCalls("hello"), fauxReply("done", ai.StopReasonStop, 0))
	if _, err := h.session.Send(t.Context(), "run tool"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !slices.Equal(seen[0], []string{"system", "user", "assistant"}) {
		t.Fatalf("roles at tool call=%v want=[[system user assistant]]", seen)
	}
}
