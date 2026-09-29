package codingagent

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type copilotCatalogTransport func(*http.Request) (*http.Response, error)

func (f copilotCatalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The production model picker consumes registry availability after the same OAuth refresh/store sequence as Models.getAvailable.
func TestCopilotAuthenticatedAccountAvailableModelsUpstream(t *testing.T) {
	models := ai.ListModels("github-copilot")
	if len(models) < 3 {
		t.Fatal("fixture requires first three models")
	}
	for _, tc := range []struct {
		name string
		rows []any
	}{
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:143
		{"filters models to the authenticated account picker catalog", []any{
			map[string]any{"id": models[0].ID, "model_picker_enabled": true, "capabilities": map[string]any{"supports": map[string]any{"tool_calls": true}}},
			map[string]any{"id": models[1].ID, "model_picker_enabled": true, "policy": map[string]any{"state": "disabled"}, "capabilities": map[string]any{"supports": map[string]any{"tool_calls": true}}},
			map[string]any{"id": models[2].ID, "model_picker_enabled": false, "policy": map[string]any{"state": "enabled"}, "capabilities": map[string]any{"supports": map[string]any{"tool_calls": true}}},
		}},
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:178
		{"falls back to explicitly enabled policy models when the picker catalog is empty", []any{
			map[string]any{"id": models[0].ID, "model_picker_enabled": false, "policy": map[string]any{"state": "enabled"}, "capabilities": map[string]any{"supports": map[string]any{"tool_calls": true}}},
			map[string]any{"id": "policy-disabled-model", "model_picker_enabled": false, "policy": map[string]any{"state": "disabled"}, "capabilities": map[string]any{"supports": map[string]any{"tool_calls": true}}},
			map[string]any{"id": "unconfigured-model", "model_picker_enabled": false, "capabilities": map[string]any{"supports": map[string]any{"tool_calls": true}}},
			map[string]any{"id": "tool-incapable-model", "model_picker_enabled": false, "policy": map[string]any{"state": "enabled"}, "capabilities": map[string]any{"supports": map[string]any{"tool_calls": false}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := modelPickerTestMode(t)
			previous := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previous })
			token := "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;"
			http.DefaultClient = &http.Client{Transport: copilotCatalogTransport(func(r *http.Request) (*http.Response, error) {
				var value any = map[string]any{"token": token, "expires_at": 9999999999}
				if strings.HasSuffix(r.URL.Path, "/models") {
					value = map[string]any{"data": tc.rows}
					if r.Header.Get("Authorization") != "Bearer "+token {
						t.Errorf("authorization=%s", r.Header.Get("Authorization"))
					}
				}
				body, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}
			method, ok := ai.OAuthProviderAuth("github-copilot")
			if !ok {
				t.Fatal("missing Copilot")
			}
			credential, err := method.Refresh(t.Context(), ai.Credential{Type: ai.CredentialOAuth, Access: "old-access-token", Refresh: "ghu_refresh_token"})
			if err != nil {
				t.Fatal(err)
			}
			storage, err := ai.NewAuthStorage(filepath.Join(mode.opts.AgentDir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = storage.Set("github-copilot", credential); err != nil {
				t.Fatal(err)
			}
			mode.opts.ModelRegistry.SetAuthStorage(storage)
			var got []string
			for _, item := range mode.availableModelItems() {
				got = append(got, item.ID)
			}
			if !reflect.DeepEqual(got, []string{models[0].ID}) {
				t.Fatalf("available=%v want authenticated model %s", got, models[0].ID)
			}
			mode.opts.ModelRegistry.SetRuntimeAPIKey("github-copilot", "runtime-key")
			if got := mode.availableModelItems(); len(got) != len(models) {
				t.Fatalf("API-key override retained OAuth filter: got %d models, catalog has %d", len(got), len(models))
			}
			mode.opts.ModelRegistry.SetRuntimeAPIKey("github-copilot", "")
			if got := mode.availableModelItems(); len(got) != 1 || got[0].ID != models[0].ID {
				t.Fatalf("empty override hid stored OAuth filter: %v", got)
			}
		})
	}
}
