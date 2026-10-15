package agent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's agent loop passes its config to the stream function without a maxTokens (agent-loop.ts streamAssistantResponse; agent.ts sets
// none). The API module's streamSimple applies buildBaseOptions (simple-options.ts:46, the model's maxTokens clamped to the context),
// and pi-messages.ts streamSimple and a caller's own stream function receive the caller's value. ai/simple_options_test.go and
// ai/estimate_test.go cover the clamp.
func TestAgentLoopLeavesMaxTokensToTheStreamFunctionLikePi(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	model := &ai.Model{ID: "mock", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 200_000, MaxOutputTokens: 64_000}}
	a := mustNewAgent(AgentOptions{Model: model})
	if _, err := a.Send(context.Background(), "new prompt"); err != nil {
		t.Fatal(err)
	}
	if got := provider.request(1).opts.MaxTokens; got != 0 {
		t.Fatalf("max_tokens = %d, want none from the agent loop", got)
	}
}
