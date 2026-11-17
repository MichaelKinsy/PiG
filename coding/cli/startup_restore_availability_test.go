package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// main.ts hands session creation the model sdk.ts:190-232 selects: a restore whose saved model is not restorable falls back to findInitialModel,
// which reads modelRuntime.getAvailableSnapshot() (model-resolver.ts:598), the models the availability pass published. A provider that narrows its
// models per credential (Provider.filterModels, models.ts:749: GitHub Copilot lists the account's availableModelIds) has only the listed models
// available, so startup selects a listed model, and none when the account lists no model of the catalog. A provider without such a filter (an
// API-key provider, an OpenAI-compatible provider at a custom base URL, a provider the default-model table does not name) offers its whole catalog.
func TestStartupRestoreFallbackSelectsFromThePublishedAvailability(t *testing.T) {
	type obj = map[string]any
	copilotOAuth := func(ids []string) obj {
		return obj{"github-copilot": obj{"type": "oauth", "refresh": "ghu_FAKEREFRESH", "access": "tid=1;exp=4102444800;proxy-ep=copilot.invalid",
			"expires": time.Now().Add(time.Hour).UnixMilli(), "availableModelIds": ids}}
	}
	lastCatalogID := func(provider string) string {
		models := ai.ListModels(provider)
		return models[len(models)-1].ID
	}
	defaultModel := func(provider string) string {
		for _, entry := range codingagent.DefaultModelPerProviderOrder {
			if entry.Provider == provider {
				return entry.ModelID
			}
		}
		t.Fatalf("no default model for %s", provider)
		return ""
	}
	customModels := obj{"providers": obj{"custom-gw": obj{"baseUrl": "https://gateway.invalid/v1", "api": "openai-completions", "apiKey": "k-custom-FAKE", "models": []obj{
		{"id": "gw-a", "name": "gw-a", "contextWindow": 32000, "maxTokens": 4000, "input": []string{"text"}},
		{"id": "gw-b", "name": "gw-b", "contextWindow": 32000, "maxTokens": 4000, "input": []string{"text"}},
	}}}}
	for _, tc := range []struct {
		name, savedProvider  string
		auth, models         obj
		wantProvider, wantID string
	}{
		{name: "oauth account lists no catalog model", savedProvider: "github-copilot", auth: copilotOAuth([]string{"zz-not-in-the-catalog"})},
		{name: "oauth account lists one catalog model", savedProvider: "github-copilot", auth: copilotOAuth([]string{lastCatalogID("github-copilot")}),
			wantProvider: "github-copilot", wantID: lastCatalogID("github-copilot")},
		{name: "api key with a custom base url", savedProvider: "groq", auth: obj{"groq": obj{"type": "api_key", "key": "gsk_FAKEKEY123456"}},
			models:       obj{"providers": obj{"groq": obj{"baseUrl": "https://gateway.invalid/openai/v1"}}},
			wantProvider: "groq", wantID: defaultModel("groq")},
		{name: "openai-compatible provider without a default model", savedProvider: "custom-gw", models: customModels, wantProvider: "custom-gw", wantID: "gw-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateProviderAuthEnv(t)
			write := func(name string, value any) {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.auth != nil {
				write("auth.json", tc.auth)
			}
			if tc.models != nil {
				write("models.json", tc.models)
			}
			services := testServices(t, dir)
			t.Cleanup(services.Close)
			manager, err := coding.NewInMemorySessionManager(services.CWD())
			if err != nil {
				t.Fatal(err)
			}
			// The saved model is one no catalog holds: the restore fails and the fallback selection runs.
			if _, err := manager.AppendModelChange(tc.savedProvider, "zz-saved-model-gone"); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "saved message"}}}}); err != nil {
				t.Fatal(err)
			}
			got, err := selectStartupModel(context.Background(), startupModelOptions{Continuing: true, SessionManager: manager}, codingagent.Settings{}, services)
			if tc.wantID == "" {
				if got.Model != nil {
					t.Fatalf("selected %s/%s, want no model: the account lists no model of the catalog", got.Model.ProviderMeta.ProviderID, got.Model.ID)
				}
				if want := codingagent.FormatNoModelsAvailableMessage(); got.ModelFallbackMessage != want {
					t.Fatalf("fallback message = %q (err %v), want %q", got.ModelFallbackMessage, err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Model == nil || got.Model.ProviderMeta.ProviderID != tc.wantProvider || got.Model.ID != tc.wantID {
				t.Fatalf("selected %+v, want %s/%s", got.Model, tc.wantProvider, tc.wantID)
			}
			if want := "Could not restore model " + tc.savedProvider + "/zz-saved-model-gone. Using " + tc.wantProvider + "/" + tc.wantID; got.ModelFallbackMessage != want {
				t.Fatalf("fallback message = %q, want %q", got.ModelFallbackMessage, want)
			}
		})
	}
}

// The same selection for a provider that narrows its models per credential by its own filter (Provider.filterModels, models.ts:749), whatever its
// name: the hidden model comes first in the catalog and has auth, yet is not available, so the fallback selects the listed one.
func TestStartupRestoreFallbackAppliesANativeProvidersFilter(t *testing.T) {
	services := testServices(t, isolateProviderAuthEnv(t))
	t.Cleanup(services.Close)
	const id = "native-filter"
	model := func(name string) *ai.Model {
		return &ai.Model{ID: name, DisplayName: name, Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: id, API: ai.APIOpenAICompletions, BaseURL: "https://custom.invalid/v1"}}
	}
	models := []*ai.Model{model("hidden"), model("visible")}
	if err := services.Auth().Set(id, ai.Credential{Type: ai.CredentialAPIKey, Key: "configured"}); err != nil {
		t.Fatal(err)
	}
	provider := &ai.ModelsProvider{ID: id, GetModels: func() ([]*ai.Model, error) { return models, nil },
		Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Check: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if input.Credential == nil {
				return nil, nil
			}
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored key"}, nil
		}}},
		FilterModels: func(models []*ai.Model, _ *ai.Credential) []*ai.Model {
			return slices.DeleteFunc(slices.Clone(models), func(m *ai.Model) bool { return m.ID == "hidden" })
		}}
	if err := services.ModelRuntime().RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := services.ModelRuntime().GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	manager, err := coding.NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModelChange(id, "zz-saved-model-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "saved message"}}}}); err != nil {
		t.Fatal(err)
	}
	got, err := selectStartupModel(context.Background(), startupModelOptions{Continuing: true, SessionManager: manager}, codingagent.Settings{}, services)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model == nil || got.Model.ProviderMeta.ProviderID != id || got.Model.ID != "visible" {
		t.Fatalf("selected %+v, want %s/visible: hidden is filtered from the availability", got.Model, id)
	}
}
