package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestSessionExplicitThinkingOverridesSettingsAndResume(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/sdk.ts:231-255,401-412 gives explicit thinking precedence without rewriting existing thinking entries.
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "resumed"}[resume], func(t *testing.T) {
			services := newTestServices(t)
			if err := services.SettingsManager().SetDefaultThinkingLevel("medium"); err != nil {
				t.Fatal(err)
			}
			if err := services.SettingsManager().SetModelThinkingLevel("anthropic", "claude-sonnet-4-5", "minimal"); err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel("anthropic/claude-sonnet-4-5", services)
			if err != nil {
				t.Fatal(err)
			}
			opts := SessionStartOptions{Model: model, ThinkingLevel: ai.ThinkingHigh, SkipBuiltinTools: true}
			rt, err := NewRuntime(RuntimeOptions{Services: services})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rt.Close() })
			before := 0
			if resume {
				previous, err := rt.New(SessionStartOptions{Model: model, ThinkingLevel: ai.ThinkingLow, SkipBuiltinTools: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := previous.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "saved"}}, StopReason: "stop"}}); err != nil {
					t.Fatal(err)
				}
				opts.ResumePath = previous.Path()
				before = len(previous.Inner().Entries())
				if err := previous.Close(); err != nil {
					t.Fatal(err)
				}
			}
			session, err := rt.New(opts)
			if err != nil {
				t.Fatal(err)
			}
			if session.ThinkingLevel() != ai.ThinkingHigh {
				t.Fatalf("thinking=%s, want high", session.ThinkingLevel())
			}
			if resume && len(session.Inner().Entries()) != before {
				t.Errorf("explicit resume thinking rewrote existing metadata: %d -> %d", before, len(session.Inner().Entries()))
			}
			if services.SettingsManager().GetDefaultThinkingLevel() != "medium" {
				t.Error("session thinking rewrote global default")
			}
		})
	}
}
