package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// The RPC message encoder writes an assistant message's members in the order Pi's providers create them (providerThinkingLevel after
// timestamp: anthropic-messages.ts:651; responseId at the first chunk, responseModel, rawStopReason, errorMessage: openai-completions.ts:564-586;
// Codex's endTurn before rawStopReason: openai-codex-responses.ts:774-778; usage splits after cost: anthropic-messages.ts:684, :851), which
// the session-line encoder writes too; that encoder is checked against Pi's own streams by agent.TestAssistantLateMembersFollowPiCreationOrder
// and agent.TestCodexAssistantEndTurnPrecedesRawStopReason.
func TestRPCAssistantMessageMembersMatchTheSessionLineOrder(t *testing.T) {
	duration, endTurn := int64(7), true
	// Usage in the order the Anthropic provider creates it: cacheWrite1h and reasoning after cost (anthropic-messages.ts:684, :851).
	var usage ai.Usage
	if err := json.Unmarshal([]byte(`{"input":5,"output":4,"cacheRead":0,"cacheWrite":3,"totalTokens":12,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0},"cacheWrite1h":3,"reasoning":2}`), &usage); err != nil {
		t.Fatal(err)
	}
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, API: ai.APIOpenAICompletions,
		Provider: "custom", ModelID: "m1", Usage: &usage, ProviderThinkingLevel: "high", StopReason: ai.StopReasonError, Timestamp: 1,
		ResponseID: "chatcmpl-1", ResponseModel: "served-model", RawStopReason: "content_filter",
		ErrorMessage: "Provider finish_reason: content_filter", EndTurn: &endTurn, DurationMs: &duration, ThinkingLevel: "high",
	}}
	session, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := rpcAgentMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rpc, session) {
		t.Fatalf("RPC assistant message\n got: %s\nwant: %s", rpc, session)
	}
}
