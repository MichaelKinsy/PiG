package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi sdk.ts:232-239 uses the global default for existing history without a thinking entry, then clamps after model selection at :252-256.
func TestPairReviewResumeWithoutThinkingEntryUsesSettings(t *testing.T) {
	for _, viaManager := range []bool{false, true} {
		name := "ResumePath"
		if viaManager {
			name = "SessionManager"
		}
		t.Run(name, func(t *testing.T) {
			services := newTestServices(t)
			if err := services.Auth().Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: "test"}); err != nil {
				t.Fatal(err)
			}
			if err := services.SettingsManager().SetDefaultThinkingLevel("high"); err != nil {
				t.Fatal(err)
			}
			// Publish fixture credentials before snapshot-only initial model selection.
			if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
				t.Fatalf("refresh=%v", refreshed)
			}
			storage := newSessionManagerForDir(services, t.TempDir())
			history, err := storage.Create("review-no-thinking", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := history.AppendModelSwitch("openai", "gpt-5", ""); err != nil {
				t.Fatal(err)
			}
			_, err = history.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "openai", ModelID: "gpt-5", API: ai.APIOpenAIResponses, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "history"}}, StopReason: ai.StopReasonStop, Usage: &ai.Usage{}}})
			if err != nil {
				t.Fatal(err)
			}
			options := SessionOptions{ResumePath: history.Path(), SkipBuiltinTools: true}
			if viaManager {
				options.ResumePath = ""
				options.SessionManager = history
			}
			session, err := NewSession(services, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
				for range session.Events() {
				}
			}()
			if got := session.ThinkingLevel(); got != ai.ThinkingHigh {
				t.Fatalf("restored model=%s thinking=%s; want global high when history has no thinking entry", session.Model().ID, got)
			}
		})
	}
}
