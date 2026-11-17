package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// sdk.ts:190-232 falls back to findInitialModel, which reads modelRuntime.getAvailableSnapshot() (model-resolver.ts:598): the models the
// availability pass published, not every model of a provider that has auth. A provider that narrows its models per credential (GitHub Copilot's
// availableModelIds, models.ts:749 filterModels) leaves a model that its account does not list unavailable, so a restore whose saved model the
// catalog no longer holds selects only a listed model, and none when the account lists none of the catalog.
func TestCreateAgentSessionFromServicesRestoreFallbackSelectsFromThePublishedAvailability(t *testing.T) {
	clearProviderEnvKeys(t)
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	type obj = map[string]any
	copilotOAuth := func(ids []string) obj {
		return obj{"github-copilot": obj{"type": "oauth", "refresh": "ghu_FAKEREFRESH", "access": "tid=1;exp=4102444800;proxy-ep=copilot.invalid",
			"expires": time.Now().Add(time.Hour).UnixMilli(), "availableModelIds": ids}}
	}
	// lastCatalogID is a model the provider's catalog holds, chosen from the catalog so the test names no model of it.
	lastCatalogID := func(provider string) string {
		models := ai.ListModels(provider)
		return models[len(models)-1].ID
	}
	defaultModel := func(provider string) string {
		for _, entry := range icodingagent.DefaultModelPerProviderOrder {
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
		name, savedProvider               string
		auth, models                      obj
		wantProvider, wantID, wantNoModel string
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
			cwd, agentDir := t.TempDir(), filepath.Join(t.TempDir(), "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.auth != nil {
				writeJSON(filepath.Join(agentDir, "auth.json"), tc.auth)
			}
			if tc.models != nil {
				writeJSON(filepath.Join(agentDir, "models.json"), tc.models)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			manager, err := NewInMemorySessionManager(services.CWD())
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
			result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: manager})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = result.Session.Close() })
			model := result.Session.Model()
			if tc.wantID == "" {
				// A session that has no model reports the unknown/unknown placeholder, never a catalog model.
				if model != nil && (model.ProviderMeta.ProviderID != "unknown" || model.ID != "unknown") {
					t.Fatalf("selected %s/%s, want no model: the account lists no model of the catalog", model.ProviderMeta.ProviderID, model.ID)
				}
				if want := icodingagent.FormatNoModelsAvailableMessage(); result.ModelFallbackMessage != want {
					t.Fatalf("fallback message = %q, want %q", result.ModelFallbackMessage, want)
				}
				return
			}
			if model == nil || model.ProviderMeta.ProviderID != tc.wantProvider || model.ID != tc.wantID {
				t.Fatalf("selected %+v, want %s/%s", model, tc.wantProvider, tc.wantID)
			}
			if want := "Could not restore model " + tc.savedProvider + "/zz-saved-model-gone. Using " + tc.wantProvider + "/" + tc.wantID; !strings.HasPrefix(result.ModelFallbackMessage, want) {
				t.Fatalf("fallback message = %q, want %q", result.ModelFallbackMessage, want)
			}
		})
	}
}

// The same selection for a provider that narrows its models per credential by its own filter (Provider.filterModels, models.ts:749), whatever its
// name: the hidden model comes first in the catalog and has auth, yet is not available, so the fallback selects the listed one.
func TestCreateAgentSessionFromServicesRestoreFallbackAppliesANativeProvidersFilter(t *testing.T) {
	clearProviderEnvKeys(t)
	services := servicesWithDefaultTools(t, nil)
	runtime := services.ModelRuntime()
	const id = "native-filter"
	models := []*ai.Model{nativeCompatModel("hidden", id, "https://custom.invalid/v1"), nativeCompatModel("visible", id, "https://custom.invalid/v1")}
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
			return slices.DeleteFunc(slices.Clone(models), func(model *ai.Model) bool { return model.ID == "hidden" })
		}}
	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.GetAvailable(t.Context()); err != nil {
		t.Fatal(err)
	}
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModelChange(id, "zz-saved-model-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "saved message"}}}}); err != nil {
		t.Fatal(err)
	}
	result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	if model := result.Session.Model(); model == nil || model.ProviderMeta.ProviderID != id || model.ID != "visible" {
		t.Fatalf("selected %+v, want %s/visible: hidden is filtered from the availability", model, id)
	}
}
