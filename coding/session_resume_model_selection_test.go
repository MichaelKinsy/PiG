package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/src/core/sdk.ts:198-205 restores a saved model only when options.model is absent. Stored usage cost is not repriced for an explicit replacement model.
func TestResumedSessionHonorsExplicitModel(t *testing.T) {
	for _, tc := range []struct{ name, spec string }{
		{"OAuth model", "github-copilot/claude-haiku-4.5"},
		{"API key model", "openai/gpt-4o"},
		{"custom endpoint without default", "custom"},
		{"omitted model restores saved selection", ""},
	} {
		for _, viaManager := range []bool{false, true} {
			mode := "ResumePath"
			if viaManager {
				mode = "SessionManager"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				services := newTestServices(t)
				if err := services.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "saved-key"}); err != nil {
					t.Fatal(err)
				}
				// Publish fixture credentials before snapshot-only initial model selection.
				if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
					t.Fatalf("refresh=%v", refreshed)
				}
				saved, err := BuildModel("anthropic/claude-opus-4-8", services)
				if err != nil {
					t.Fatal(err)
				}
				source, err := NewSession(services, SessionOptions{Model: saved, SkipBuiltinTools: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := source.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}}); err != nil {
					t.Fatal(err)
				}
				if _, err := source.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-opus-4-8", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, StopReason: ai.StopReasonStop, Usage: &ai.Usage{Input: 10000, Output: 2000, CacheRead: 30000, TotalTokens: 42000, Cost: ai.UsageCost{Input: 0.05, Output: 0.05, CacheRead: 0.015, Total: 0.115}}}}); err != nil {
					t.Fatal(err)
				}
				path := source.Path()
				manager := source.Inner()
				if err := source.Close(); err != nil {
					t.Fatal(err)
				}
				for range source.Events() {
				}
				var requested *ai.Model
				switch tc.spec {
				case "":
				case "custom":
					requested = &ai.Model{ID: "no-default", DisplayName: "Custom endpoint", ProviderMeta: ai.ProviderMetadata{ProviderID: "custom-proxy", API: ai.APIOpenAICompletions, BaseURL: "http://127.0.0.1:9/v1"}}
				default:
					requested, err = BuildModel(tc.spec, services)
					if err != nil {
						t.Fatal(err)
					}
				}
				options := SessionOptions{Model: requested, ResumePath: path, SkipBuiltinTools: true}
				if viaManager {
					options.ResumePath = ""
					options.SessionManager = manager
				}
				resumed, err := NewSession(services, options)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := resumed.Close(); err != nil {
						t.Error(err)
					}
					for range resumed.Events() {
					}
				})
				want := requested
				if want == nil {
					want = saved
				}
				if got := resumed.Model(); got == nil || got.ID != want.ID || got.ProviderMeta.ProviderID != want.ProviderMeta.ProviderID {
					t.Fatalf("resumed model=%+v, want %s/%s", got, want.ProviderMeta.ProviderID, want.ID)
				}
				if got := resumed.GetSessionStats().Cost; got != 0.115 {
					t.Fatalf("stored cost=%v, want 0.115", got)
				}
			})
		}
	}
}
