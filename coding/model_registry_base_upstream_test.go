package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func registryModelsForProvider(registry *ModelRegistry, id string) []icodingagent.ModelEntry {
	var models []icodingagent.ModelEntry
	for _, model := range registry.GetAll() {
		if model.ProviderID == id {
			models = append(models, model)
		}
	}
	return models
}
func writeRegistryModels(t *testing.T, services *Services, providers map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(services.AgentDir(), "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func registryProviderConfig(baseURL, api string, ids ...string) map[string]any {
	var models []any
	for _, id := range ids {
		models = append(models, map[string]any{"id": id, "name": id, "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 100000, "maxTokens": 8000})
	}
	return map[string]any{"baseUrl": baseURL, "apiKey": "test-key", "api": api, "models": models}
}

func TestModelRegistryBaseOverridesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config map[string]any
		check  func(*testing.T, *Services)
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:95
		{"overriding baseUrl keeps all built-in models", map[string]any{"anthropic": map[string]any{"baseUrl": "https://my-proxy.example.com/v1"}}, func(t *testing.T, s *Services) {
			models := registryModelsForProvider(s.Registry(), "anthropic")
			if len(models) <= 1 {
				t.Fatal("builtin catalog lost")
			}
			found := false
			for _, m := range models {
				if len(m.ModelID) >= 6 && m.ModelID[:6] == "claude" {
					found = true
				}
			}
			if !found {
				t.Fatal("Claude models lost")
			}
		}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:108
		{"overriding baseUrl changes URL on all built-in models", map[string]any{"anthropic": map[string]any{"baseUrl": "https://my-proxy.example.com/v1"}}, func(t *testing.T, s *Services) {
			for _, m := range registryModelsForProvider(s.Registry(), "anthropic") {
				if m.BaseURL != "https://my-proxy.example.com/v1" {
					t.Errorf("baseUrl=%q", m.BaseURL)
				}
			}
		}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:122
		{"overriding headers resolves at request time", map[string]any{"anthropic": map[string]any{"baseUrl": "https://my-proxy.example.com/v1", "headers": map[string]string{"X-Custom-Header": "custom-value"}}}, func(t *testing.T, s *Services) {
			for _, m := range registryModelsForProvider(s.Registry(), "anthropic") {
				auth := s.Registry().GetAPIKeyAndHeaders(t.Context(), s.Registry().Find(m.ProviderID, m.ModelID))
				if !auth.OK || auth.Headers["X-Custom-Header"] == nil || *auth.Headers["X-Custom-Header"] != "custom-value" {
					t.Errorf("auth=%+v", auth)
				}
			}
		}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:141
		{"headers-only override resolves at request time", map[string]any{"anthropic": map[string]any{"headers": map[string]string{"X-Custom-Header": "custom-value"}}}, func(t *testing.T, s *Services) {
			if s.Registry().GetError() != "" {
				t.Fatal(s.Registry().GetError())
			}
			for _, m := range registryModelsForProvider(s.Registry(), "anthropic") {
				auth := s.Registry().GetAPIKeyAndHeaders(t.Context(), s.Registry().Find(m.ProviderID, m.ModelID))
				if !auth.OK || auth.Headers["X-Custom-Header"] == nil || *auth.Headers["X-Custom-Header"] != "custom-value" {
					t.Errorf("auth=%+v", auth)
				}
			}
		}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:177
		{"baseUrl-only override does not affect other providers", map[string]any{"anthropic": map[string]any{"baseUrl": "https://my-proxy.example.com/v1"}}, func(t *testing.T, s *Services) {
			models := registryModelsForProvider(s.Registry(), "google")
			if len(models) == 0 || models[0].BaseURL == "https://my-proxy.example.com/v1" {
				t.Fatalf("Google models=%+v", models)
			}
		}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:190
		{"can mix baseUrl override and models merge", map[string]any{"anthropic": map[string]any{"baseUrl": "https://anthropic-proxy.example.com/v1"}, "google": registryProviderConfig("https://google-proxy.example.com/v1", "google-generative-ai", "gemini-custom")}, func(t *testing.T, s *Services) {
			anthropic := registryModelsForProvider(s.Registry(), "anthropic")
			google := registryModelsForProvider(s.Registry(), "google")
			if len(anthropic) <= 1 || anthropic[0].BaseURL != "https://anthropic-proxy.example.com/v1" || len(google) <= 1 || s.Registry().Find("google", "gemini-custom") == nil {
				t.Fatal("mixed override lost models")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.check(t, registryTestServices(t, "", tc.config)) })
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:163
	t.Run("unconfigured compatibility auth includes static model headers", func(t *testing.T) {
		registry := registryTestServices(t, "", nil).Registry()
		base := registry.GetAll()[0]
		model := registry.Find(base.ProviderID, base.ModelID)
		model.ProviderMeta.ProviderID = "missing-provider"
		model.ProviderMeta.Headers = map[string]string{"X-Static-Model": "static-value"}
		auth := registry.GetAPIKeyAndHeaders(t.Context(), model)
		if !auth.OK || auth.APIKey != nil || len(auth.Headers) != 1 || auth.Headers["X-Static-Model"] == nil || *auth.Headers["X-Static-Model"] != "static-value" {
			t.Fatalf("auth=%+v", auth)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:215
	t.Run("refresh() picks up baseUrl override changes", func(t *testing.T) {
		services := registryTestServices(t, "", map[string]any{"anthropic": map[string]any{"baseUrl": "https://first-proxy.example.com/v1"}})
		if registryModelsForProvider(services.Registry(), "anthropic")[0].BaseURL != "https://first-proxy.example.com/v1" {
			t.Fatal("initial URL missing")
		}
		writeRegistryModels(t, services, map[string]any{"anthropic": map[string]any{"baseUrl": "https://second-proxy.example.com/v1"}})
		result := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
		if result.Aborted || len(result.Errors) > 0 {
			t.Fatalf("refresh=%+v", result)
		}
		if registryModelsForProvider(services.Registry(), "anthropic")[0].BaseURL != "https://second-proxy.example.com/v1" {
			t.Fatal("refreshed URL missing")
		}
	})
}
