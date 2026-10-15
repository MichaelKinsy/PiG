package agent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 1.2.0 agent-session.ts:1104-1106 aborts a run at agent_start when an abort was requested while it started; agent.ts runWithLifecycle
// then hands the provider an aborted signal before the first request, so no request goes out and the run ends with an aborted response.
// The abort reaches the run whether it comes from an agent_start listener, right after Start, or from a context cancelled before the claim.
func TestAbortBeforeTheFirstRequestSendsNoProviderRequest(t *testing.T) {
	user := []AgentMessage{{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}}}
	for _, abort := range []string{"agent_start listener", "after Start", "cancelled context"} {
		t.Run(abort, func(t *testing.T) {
			server, bodies := nativeToolChangeServer(t, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			provider := ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "sk-ant-api03-test", Model: "custom-claude", BaseURL: server.URL})
			a := mustNewAgent(AgentOptions{Model: &ai.Model{ID: "custom-claude", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: ai.APIAnthropicMessages}, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}})
			if abort == "agent_start listener" {
				a.Subscribe(func(_ context.Context, event AgentEvent) error {
					if _, ok := event.(AgentStartEvent); ok {
						a.Abort()
					}
					return nil
				})
			}
			ctx := t.Context()
			if abort == "cancelled context" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			run, err := a.BeginSendMessages(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			_ = run.Start()
			if abort == "after Start" {
				a.Abort()
			}
			_, err = run.Run()
			if abort != "cancelled context" && err != nil {
				t.Fatalf("Run = %v, want the aborted run to settle", err)
			}
			if n := len(bodies()); n != 0 {
				t.Errorf("provider requests = %d, want none", n)
			}
			messages := a.Messages()
			last := messages[len(messages)-1]
			if last.Assistant == nil || last.Assistant.StopReason != ai.StopReasonAborted {
				t.Errorf("last message = %+v, want an aborted response", last)
			}
		})
	}
}
