package coding

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// savedSessionPath persists a session whose model_change names provider/modelID, which no catalog describes, followed by one exchange.
func savedSessionPath(t *testing.T, services *AgentSessionServices, provider, modelID string, api ai.API) string {
	t.Helper()
	ghost := &ai.Model{ID: modelID, ProviderMeta: ai.ProviderMetadata{ProviderID: provider, API: api, BaseURL: "http://127.0.0.1:9/v1"}}
	source, err := NewSession(services, SessionOptions{Model: ghost, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: api, Provider: provider, ModelID: modelID, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, StopReason: ai.StopReasonStop}},
	} {
		if _, err := source.Inner().AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	path := source.Path()
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	for range source.Events() {
	}
	return path
}

func authRestoreServices(t *testing.T) *AgentSessionServices {
	t.Helper()
	services := remoteCatalogServices(t)
	if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: "tid=test;exp=9999999999;", Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: "openai-key"}); err != nil {
		t.Fatal(err)
	}
	if refreshed := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
		t.Fatalf("refresh=%v", refreshed)
	}
	return services
}

// sdk.ts:224-231 restores a saved model only when modelRuntime.getModel finds it. A model no catalog describes (bundled, remote, models.json) is not
// restored whatever the provider's auth shape, so the Session never carries a model with no window and no prices (the footer's 0.0%/0).
func TestResumedSessionDoesNotBuildABlankModelForAnUncatalogedSavedModel(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		api            ai.API
	}{
		{"OAuth provider", "github-copilot", ai.APIAnthropicMessages},
		{"API-key provider", "openai", ai.APIOpenAIResponses},
		{"models.json custom base URL provider", "custom-proxy", ai.APIOpenAICompletions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := authRestoreServices(t)
			path := savedSessionPath(t, services, tc.provider, "no-catalog-has-this", tc.api)
			resumed, err := NewSession(services, SessionOptions{ResumePath: path, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = resumed.Close()
				for range resumed.Events() {
				}
			})
			if got := resumed.Model(); got == nil || got.ID != "unknown" || got.ProviderMeta.ProviderID != "unknown" {
				t.Errorf("resumed on %+v, want the unknown placeholder, not a blank %s model", got, tc.provider)
			}
			replacement, err := NewSession(services, SessionOptions{Model: &ai.Model{ID: "keep", ProviderMeta: ai.ProviderMetadata{ProviderID: "custom-proxy", API: ai.APIOpenAICompletions, BaseURL: "http://127.0.0.1:9/v1"}}, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = replacement.Close()
				for range replacement.Events() {
				}
			})
			replacement.ReplaceInner(resumed.Inner())
			if got := replacement.Model(); got == nil || got.ID != "keep" {
				t.Errorf("replacement switched to %+v, want it to keep its model", got)
			}
		})
	}
}

// A models.json entry is a catalog: its model restores with the window it declares.
func TestResumedSessionRestoresAModelsJSONModel(t *testing.T) {
	services := authRestoreServices(t)
	path := savedSessionPath(t, services, "custom-proxy", "with-window", ai.APIOpenAICompletions)
	resumed, err := NewSession(services, SessionOptions{ResumePath: path, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = resumed.Close()
		for range resumed.Events() {
		}
	})
	if got := resumed.Model(); got == nil || got.ID != "with-window" || got.Capabilities.ContextWindow != 64_000 {
		t.Errorf("resumed on %+v, want custom-proxy/with-window with a 64000 window", got)
	}
}

// sdk.ts:225-249 on the SDK path: the failed restore is reported with the default model that replaces it, and with no default the no-models message.
func TestCreateAgentSessionReportsAnUncatalogedSavedModelForEveryProviderShape(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		api            ai.API
	}{
		{"OAuth provider", "github-copilot", ai.APIAnthropicMessages},
		{"API-key provider", "openai", ai.APIOpenAIResponses},
		{"models.json custom base URL provider", "custom-proxy", ai.APIOpenAICompletions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := authRestoreServices(t)
			path := savedSessionPath(t, services, tc.provider, "no-catalog-has-this", tc.api)
			if err := services.SettingsManager().SetDefaultModelAndProvider("custom-proxy", "with-window"); err != nil {
				t.Fatal(err)
			}
			manager, err := SessionManagerFor(services, SessionStartOptions{ResumePath: path})
			if err != nil {
				t.Fatal(err)
			}
			result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: manager})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = result.Session.Close()
				for range result.Session.Events() {
				}
			})
			if want := "Could not restore model " + tc.provider + "/no-catalog-has-this. Using custom-proxy/with-window"; result.ModelFallbackMessage != want {
				t.Errorf("fallback message = %q, want %q", result.ModelFallbackMessage, want)
			}
			if got := result.Session.Model(); got == nil || got.ID != "with-window" || got.Capabilities.ContextWindow != 64_000 {
				t.Errorf("continued on %+v, want custom-proxy/with-window with its 64000 window", got)
			}
		})
	}
}
