package ai

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCopilotRefreshAccountCatalogUpstream(t *testing.T) {
	ids := copilotTestModelIDs(t)
	for _, tc := range []struct {
		name, host string
		data       []any
		want       []string
	}{
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:143 (availability caller: internal/codingagent/copilot_catalog_upstream_test.go)
		{"filters models to the authenticated account picker catalog", "proxy.individual.githubcopilot.com", []any{copilotAccountRow(ids[0], true, "", new(true)), copilotAccountRow(ids[1], true, "disabled", new(true)), copilotAccountRow(ids[2], false, "enabled", new(true))}, []string{ids[0]}},
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:178 (availability caller: internal/codingagent/copilot_catalog_upstream_test.go)
		{"falls back to explicitly enabled policy models when the picker catalog is empty", "proxy.individual.githubcopilot.com", []any{copilotAccountRow(ids[0], false, "enabled", new(true)), copilotAccountRow("policy-disabled-model", false, "disabled", new(true)), copilotAccountRow("unconfigured-model", false, "", new(true)), copilotAccountRow("tool-incapable-model", false, "enabled", new(false))}, []string{ids[0]}},
		// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:216
		{"does not fall back to policy models for non-Individual accounts", "proxy.business.githubcopilot.com", []any{copilotAccountRow("gpt-4.1", false, "enabled", new(true))}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := "tid=test;exp=9999999999;proxy-ep=" + tc.host + ";"
			modelsURL := "https://" + strings.Replace(tc.host, "proxy.", "api.", 1) + "/models"
			calls := 0
			previous := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previous })
			http.DefaultClient = &http.Client{Transport: metaRoundTripper(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/copilot_internal/v2/token") {
					return metaJSONResponse(200, map[string]any{"token": token, "expires_at": 9999999999}), nil
				}
				if r.URL.String() != modelsURL {
					t.Errorf("unexpected URL %s", r.URL)
				}
				calls++
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Errorf("auth header=%q", r.Header.Get("Authorization"))
				}
				return metaJSONResponse(200, map[string]any{"data": tc.data}), nil
			})}
			method, ok := OAuthProviderAuth("github-copilot")
			if !ok {
				t.Fatal("missing Copilot")
			}
			got, err := method.Refresh(t.Context(), Credential{Type: CredentialOAuth, Access: "old-access-token", Refresh: "ghu_refresh_token"})
			var availableIDs []string
			decodeErr := json.Unmarshal(got.AvailableModelIDs, &availableIDs)
			if err != nil || decodeErr != nil || !reflect.DeepEqual(availableIDs, tc.want) || calls != 1 {
				t.Fatalf("refresh=%#v,%v catalog calls=%d", got, err, calls)
			}
			store := NewInMemoryAuthStorage(nil)
			if _, err := store.Modify(t.Context(), "github-copilot", func(*Credential) (*Credential, error) { return &got, nil }); err != nil {
				t.Fatal(err)
			}
			stored, err := store.Read(t.Context(), "github-copilot")
			if err != nil || !reflect.DeepEqual(stored, &got) {
				t.Fatalf("stored=%#v,%v", stored, err)
			}
			// The real model-picker caller is exercised by TestCopilotAuthenticatedAccountAvailableModelsUpstream in internal/codingagent.
		})
	}
}

// .upstream/v0.87.1/packages/ai/test/github-copilot-oauth.test.ts:232
func TestCopilotRefreshDoesNotRetryCatalogThrottlingUpstream(t *testing.T) {
	calls := 0
	previous := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = previous })
	http.DefaultClient = &http.Client{Transport: metaRoundTripper(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/copilot_internal/v2/token") {
			return metaJSONResponse(200, map[string]any{"token": copilotUpstreamAccess, "expires_at": 9999999999}), nil
		}
		if r.URL.String() != copilotUpstreamModelsURL {
			t.Errorf("unexpected URL %s", r.URL)
		}
		calls++
		response := metaJSONResponse(429, map[string]any{"error": "too many requests"})
		response.Status = "429"
		response.Header.Set("Retry-After", "0")
		return response, nil
	})}
	method, ok := OAuthProviderAuth("github-copilot")
	if !ok {
		t.Fatal("missing Copilot")
	}
	_, err := method.Refresh(t.Context(), Credential{Type: CredentialOAuth, Access: "old-access-token", Refresh: "ghu_refresh_token"})
	if err == nil || !strings.Contains(err.Error(), "429") || calls != 1 {
		t.Fatalf("refresh=%v calls=%d", err, calls)
	}
}
