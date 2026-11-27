package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitHubCopilotClassifierFiltering(t *testing.T) {
	t.Run("parseGitHubCopilotModelCatalog records non-disabled decision models", func(t *testing.T) {
		body := []byte(`{
			"data": [
				{"id": "gpt-5", "model_picker_enabled": true, "capabilities": {"type": "chat"}, "policy": {"state": "enabled"}},
				{"id": "gpt-6-luna-decisions", "model_picker_enabled": false, "capabilities": {"type": "decisions"}, "policy": {"state": "enabled"}},
				{"id": "microsoft-decision-1", "model_picker_enabled": false, "capabilities": {"type": "decisions"}, "policy": {"state": "disabled"}}
			]
		}`)
		catalog, err := parseGitHubCopilotModelCatalog(body, false)
		if err != nil {
			t.Fatalf("parseGitHubCopilotModelCatalog: %v", err)
		}
		if len(catalog.AvailableModelIDs) != 1 || catalog.AvailableModelIDs[0] != "gpt-5" {
			t.Fatalf("AvailableModelIDs = %v, want [gpt-5]", catalog.AvailableModelIDs)
		}
		if len(catalog.ClassifierModelIDs) != 1 || catalog.ClassifierModelIDs[0] != "gpt-6-luna-decisions" {
			t.Fatalf("ClassifierModelIDs = %v, want [gpt-6-luna-decisions]", catalog.ClassifierModelIDs)
		}
	})

	t.Run("full provider + auth store round-trip filters classifiers and preserves legacy credentials", func(t *testing.T) {
		authPath := filepath.Join(t.TempDir(), "auth.json")
		store, err := NewAuthStorage(authPath)
		if err != nil {
			t.Fatalf("NewAuthStorage: %v", err)
		}
		cred := &Credential{
			Type:                        CredentialOAuth,
			Access:                      "tid=test;exp=9999999999;proxy-ep=proxy.business.githubcopilot.com;",
			Refresh:                     "refresh",
			Expires:                     time.Now().UnixMilli() + 3_600_000,
			AvailableModelIDs:           json.RawMessage(`["gpt-6-luna"]`),
			AvailableClassifierModelIDs: json.RawMessage(`["gpt-6-luna-decisions"]`),
		}
		if _, err := store.Modify(t.Context(), "github-copilot", func(*Credential) (*Credential, error) {
			return cred, nil
		}); err != nil {
			t.Fatalf("store.Modify: %v", err)
		}
		// Reload through a fresh store to prove auth.go + auth_store.go + auth_providers.go persist AvailableClassifierModelIDs.
		reloaded, err := NewAuthStorage(authPath)
		if err != nil {
			t.Fatalf("reload NewAuthStorage: %v", err)
		}
		stored, found, err := ReadRawCredential(t.Context(), reloaded, "github-copilot")
		if err != nil || !found {
			t.Fatalf("ReadRawCredential: found=%v err=%v", found, err)
		}
		if string(stored.AvailableClassifierModelIDs) != `["gpt-6-luna-decisions"]` {
			t.Fatalf("persisted AvailableClassifierModelIDs = %s", string(stored.AvailableClassifierModelIDs))
		}
		oauthView := credentialToOAuth(stored)
		if string(oauthView.Extra["availableClassifierModelIds"]) != `["gpt-6-luna-decisions"]` {
			t.Fatalf("credentialToOAuth missing availableClassifierModelIds: %v", oauthView)
		}

		// Exercise BuiltinModels / builtinFilterAllModels end-to-end: microsoft-decision-1 is excluded by AvailableClassifierModelIDs.
		models := BuiltinModels(CreateModelsOptions{Credentials: reloaded})
		avail, err := models.GetAvailableOfType(t.Context(), ModelTypeClassifier, "github-copilot")
		if err != nil {
			t.Fatalf("GetAvailableOfType: %v", err)
		}
		for _, m := range avail {
			if m.ModelID() == "microsoft-decision-1" {
				t.Fatalf("expected microsoft-decision-1 filtered out by AvailableClassifierModelIDs, got %v", avail)
			}
		}

		// Legacy credential without availableClassifierModelIds keeps all classifiers via the full provider path.
		legacyStore := NewInMemoryCredentialStore()
		if _, err := legacyStore.Modify(t.Context(), "github-copilot", func(*Credential) (*Credential, error) {
			return &Credential{
				Type:              CredentialOAuth,
				Access:            "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;",
				Refresh:           "refresh",
				Expires:           time.Now().UnixMilli() + 3_600_000,
				AvailableModelIDs: json.RawMessage(`["gpt-6-luna"]`),
			}, nil
		}); err != nil {
			t.Fatal(err)
		}
		legacyModels := BuiltinModels(CreateModelsOptions{Credentials: legacyStore})
		legacyAvail, err := legacyModels.GetAvailableOfType(t.Context(), ModelTypeClassifier, "github-copilot")
		if err != nil || len(legacyAvail) == 0 {
			t.Fatalf("expected legacy credential without classifier list to keep classifiers, got %v, err=%v", legacyAvail, err)
		}
	})

	t.Run("routes Copilot openai-decisions (gpt-6-luna-decisions) to /v1/decisions", func(t *testing.T) {
		var gotPath, gotAuth, gotIntegration string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotIntegration = r.Header.Get("Copilot-Integration-Id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":[{"name":"approved","type":"predicate","probability":0.8}]}`))
		}))
		defer srv.Close()

		cls := builtinClassifiers("github-copilot")
		impl := cls[ClassifierAPIOpenAIDecisions]
		if impl == nil {
			t.Fatal("expected openai-decisions classifier on github-copilot")
		}
		model := &ClassifierModel{
			ID:       "gpt-6-luna-decisions",
			API:      ClassifierAPIOpenAIDecisions,
			Provider: "github-copilot",
			BaseURL:  srv.URL,
			Headers:  map[string]string{"Copilot-Integration-Id": "vscode-chat"},
			Input:    []string{"text", "image"},
		}
		res, err := impl.Classify(context.Background(), model, ClassifierContext{
			State:     JsonObject{"text": "ok"},
			Questions: ClassifierQuestions{{ID: "approved", Question: ClassifierBoolQuestion{Instructions: "ok?", Criteria: ClassifierBoolCriteria{True: "yes", False: "no"}}}},
		}, ClassifierOptions{APIKey: "copilot-tok", APIKeySet: true})
		if err != nil || res.StopReason != ClassifierStopReasonStop {
			t.Fatalf("classify = %+v, err=%v", res, err)
		}
		if gotPath != "/v1/decisions" {
			t.Fatalf("path = %q, want /v1/decisions", gotPath)
		}
		if gotAuth != "Bearer copilot-tok" || !strings.EqualFold(gotIntegration, "vscode-chat") {
			t.Fatalf("auth=%q integration=%q", gotAuth, gotIntegration)
		}
	})

	// N5: Full Copilot refresh against a fake HTTP /models endpoint records AvailableClassifierModelIDs on the credential.
	t.Run("refreshCopilotToken against fake HTTP /models records AvailableClassifierModelIDs", func(t *testing.T) {
		previous := http.DefaultClient
		http.DefaultClient = &http.Client{Transport: metaRoundTripper(func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/copilot_internal/v2/token"):
				return metaJSONResponse(200, map[string]any{
					"token":      "tid=refreshed;exp=9999999999;proxy-ep=proxy.business.githubcopilot.com;",
					"expires_at": time.Now().Add(time.Hour).Unix(),
				}), nil
			case strings.HasSuffix(r.URL.Path, "/models"):
				return metaJSONResponse(200, map[string]any{
					"data": []any{
						map[string]any{"id": "gpt-6-luna", "model_picker_enabled": true, "capabilities": map[string]any{"type": "chat", "supports": map[string]any{"tool_calls": true}}},
						map[string]any{"id": "gpt-6-luna-decisions", "model_picker_enabled": false, "capabilities": map[string]any{"type": "decisions", "supports": map[string]any{}}},
						map[string]any{"id": "microsoft-decision-1", "model_picker_enabled": false, "policy": map[string]any{"state": "disabled"}, "capabilities": map[string]any{"type": "decisions", "supports": map[string]any{}}},
					},
				}), nil
			default:
				return metaJSONResponse(404, map[string]any{"error": "unexpected " + r.URL.String()}), nil
			}
		})}
		t.Cleanup(func() { http.DefaultClient = previous })

		cred, err := refreshCopilotToken(t.Context(), "gho_test_token", "")
		if err != nil {
			t.Fatalf("refreshCopilotToken: %v", err)
		}
		if string(cred.AvailableModelIDs) != `["gpt-6-luna"]` {
			t.Fatalf(`AvailableModelIDs = %s, want ["gpt-6-luna"]`, string(cred.AvailableModelIDs))
		}
		if string(cred.AvailableClassifierModelIDs) != `["gpt-6-luna-decisions"]` {
			t.Fatalf(`AvailableClassifierModelIDs = %s, want ["gpt-6-luna-decisions"]`, string(cred.AvailableClassifierModelIDs))
		}
	})
}
