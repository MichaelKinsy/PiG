package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// pi: packages/ai/src/auth/oauth/{kimi-coding,xai,openai-chatgpt,openai-codex,openrouter,radius}.ts

// Pi's providers build a credential as an object literal, and auth.json keeps its member order: these providers write access before refresh
// (anthropic, github-copilot and meta write refresh first). Each builder carries its literal's order to the credential it persists.
func credentialMemberNames(t *testing.T, creds OAuthCredentials) []string {
	t.Helper()
	credential, err := credentialFromOAuth(creds)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := authStorageObjectKeys(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestOAuthBuildersWriteCredentialMembersInPiOrder(t *testing.T) {
	token := func(name string, creds OAuthCredentials, err error, want ...string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := credentialMemberNames(t, creds); !slices.Equal(got, want) {
			t.Errorf("%s members = %v, want %v", name, got, want)
		}
	}
	kimi, err := kimiCredentials(kimiTokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 60})
	token("kimi-coding.ts:278", kimi, err, "type", "access", "refresh", "expires")

	refresh := "r"
	seconds := 60.0
	xai, err := xaiOAuthProvider{now: time.Now}.credentialsFromToken(xaiBody{AccessToken: "a", RefreshToken: &refresh, ExpiresIn: &seconds}, "")
	token("xai.ts:138", xai, err, "type", "access", "refresh", "expires")

	chatgpt, err := chatgptCredentialFromTokenResponse(chatgptTokenBody{
		"access_token": json.RawMessage(`"a"`), "refresh_token": json.RawMessage(`"r"`), "expires_in": json.RawMessage(`60`),
		"scope": json.RawMessage(`"` + chatgptDirectTokenScope + `"`),
	}, "client")
	token("openai-chatgpt.ts:174", chatgpt, err, "type", "access", "refresh", "expires", "clientId", "scopes")

	withOpenRouterToken(t, func(*http.Request) (*http.Response, error) { return cannedResp(200, `{"key":"sk-or"}`), nil })
	openrouter, err := exchangeOpenRouterCode(t.Context(), "c", "v")
	token("openrouter.ts:106", openrouter, err, "type", "access", "refresh", "expires")

	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return cannedResp(200, `{"access_token":"a","refresh_token":"r","expires_in":60}`), nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	codex, err := postCodexTokenForm(t.Context(), formFields{{"grant_type", "refresh_token"}}, "refresh")
	token("openai-codex.ts:324", codex, err, "type", "access", "refresh", "expires")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRadiusJSON(w, 200, map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 3600, "scope": "gateway"})
	}))
	defer gateway.Close()
	radius, err := newTestRadiusOAuth(gateway.URL, time.Now()).requestOAuthToken(t.Context(), formFields{{"grant_type", "refresh_token"}})
	token("radius.ts:126", radius, err, "type", "access", "refresh", "expires", "scope")
	if !strings.Contains(strings.Join(codexCredentialMembers, ","), "accountId") {
		t.Errorf("the codex credential literal ends with accountId: %v", codexCredentialMembers)
	}
}

// pi: packages/ai/src/auth/oauth/{anthropic,meta,github-copilot}.ts

// The refresh-first providers: anthropic.ts:132-137 and :270-275 (login and refresh), meta.ts:173 (login and refresh both mint) and github-copilot.ts:341-347, to which login and refresh add availableModelIds (:481-484, :363-366).
func TestRefreshFirstOAuthBuildersWriteCredentialMembersInPiOrder(t *testing.T) {
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		if strings.Contains(r.URL.Path, "copilot_internal") {
			return cannedResp(200, `{"token":"tid=1;proxy-ep=proxy.individual.githubcopilot.com","expires_at":2000000000}`), nil
		}
		return cannedResp(200, `{"access_token":"a","refresh_token":"r","expires_in":3600}`), nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	members := func(name string, creds OAuthCredentials, err error, want ...string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := credentialMemberNames(t, creds); !slices.Equal(got, want) {
			t.Errorf("%s members = %v, want %v", name, got, want)
		}
	}
	refreshFirst := []string{"type", "refresh", "access", "expires"}
	login, err := exchangeAnthropicCode(t.Context(), "c", "s", "v", "http://localhost/callback")
	members("anthropic.ts:132", login, err, refreshFirst...)
	refreshed, err := RefreshAnthropicToken(t.Context(), "r")
	members("anthropic.ts:270", refreshed, err, refreshFirst...)

	mint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_key":"k"}`))
	}))
	defer mint.Close()
	meta := newMetaOAuthProvider()
	meta.apiKeyMintURL = mint.URL
	minted, err := meta.mintAPIKey(t.Context(), "identity")
	members("meta.ts:173", minted, err, refreshFirst...)

	// github-copilot.ts:341-347 writes enterpriseUrl before availableModelIds; a codec order would sort them the other way.
	for _, tc := range []struct {
		domain string
		want   []string
	}{
		{"", []string{"type", "refresh", "access", "expires", "availableModelIds"}},
		{"ghe.example.com", []string{"type", "refresh", "access", "expires", "enterpriseUrl", "availableModelIds"}},
	} {
		copilot, err := refreshCopilotAccessToken(t.Context(), "gh", tc.domain)
		if err != nil {
			t.Fatal(err)
		}
		copilot.AvailableModelIDs = json.RawMessage(`["m"]`)
		encoded, err := json.Marshal(copilot)
		if err != nil {
			t.Fatal(err)
		}
		if keys, err := authStorageObjectKeys(encoded); err != nil || !slices.Equal(keys, tc.want) {
			t.Errorf("github-copilot.ts:341 domain %q members = %v, %v; want %v", tc.domain, keys, err, tc.want)
		}
	}
}
