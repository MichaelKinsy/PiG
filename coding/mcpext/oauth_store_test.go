package mcpext_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// Ports packages/coding-agent/test/mcp-oauth-store.test.ts.

const storeServerURL = "https://mcp.example.com/mcp"

func storeState(accessToken string) oauth.McpOAuthState {
	return oauth.McpOAuthState{ServerURL: storeServerURL, Tokens: &oauth.OAuthTokens{AccessToken: accessToken, TokenType: "Bearer"}}
}

// storedKeys is Object.keys(JSON.parse(current ?? "{}")): the top-level keys in document order.
func storedKeys(t *testing.T, backend *mcpext.InMemoryAuthStorageBackend) []string {
	t.Helper()
	var content string
	if err := backend.WithLock(func(current string, exists bool) (*string, error) {
		content = current
		if !exists {
			content = "{}"
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, token.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// seedLegacy stores state under the server URL alone, as versions before per-server keys did.
func seedLegacy(t *testing.T, backend *mcpext.InMemoryAuthStorageBackend) {
	t.Helper()
	raw, err := json.Marshal(map[string]oauth.McpOAuthState{storeServerURL: storeState("legacy-token")})
	if err != nil {
		t.Fatal(err)
	}
	next := string(raw)
	if err := backend.WithLock(func(string, bool) (*string, error) { return &next, nil }); err != nil {
		t.Fatal(err)
	}
}

func forServer(t *testing.T, store *mcpext.McpOAuthCredentialStore, name string) mcpext.McpOAuthServerStore {
	t.Helper()
	server, err := store.ForServer(name, storeServerURL)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func loadedAccessToken(t *testing.T, server mcpext.McpOAuthServerStore) *string {
	t.Helper()
	state, err := server.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		return nil
	}
	if state.Tokens == nil {
		return new("")
	}
	return &state.Tokens.AccessToken
}

func tokenOf(tokens *oauth.OAuthTokens) string {
	if tokens == nil {
		return "<none>"
	}
	return tokens.AccessToken
}

func remove(t *testing.T, store *mcpext.McpOAuthCredentialStore, name string) bool {
	t.Helper()
	removed, err := store.Remove(name, storeServerURL)
	if err != nil {
		t.Fatal(err)
	}
	return removed
}

// https://github.com/earendil-works/pi/issues/10252
func TestMCPOAuthCredentialStoreKeepsSeparateCredentialsForServersSharingAServerURL(t *testing.T) {
	ctx := t.Context()
	store := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "")
	if err := forServer(t, store, "work").Save(ctx, storeState("work-token")); err != nil {
		t.Fatal(err)
	}
	if err := forServer(t, store, "personal").Save(ctx, storeState("personal-token")); err != nil {
		t.Fatal(err)
	}

	if got := loadedAccessToken(t, forServer(t, store, "work")); got == nil || *got != "work-token" {
		t.Fatalf("work token = %v", derefOr(got))
	}
	if got := loadedAccessToken(t, forServer(t, store, "personal")); got == nil || *got != "personal-token" {
		t.Fatalf("personal token = %v", derefOr(got))
	}

	if !remove(t, store, "work") {
		t.Fatal("remove(work) = false")
	}
	if got := loadedAccessToken(t, forServer(t, store, "work")); got != nil {
		t.Fatalf("work token after remove = %v", *got)
	}
	if got := tokenOf(store.Tokens("personal", storeServerURL)); got != "personal-token" {
		t.Fatalf("personal tokens = %s", got)
	}
}

func TestMCPOAuthCredentialStoreMovesCredentialsStoredByServerURLToTheFirstServerThatLoadsThem(t *testing.T) {
	backend := &mcpext.InMemoryAuthStorageBackend{}
	seedLegacy(t, backend)
	store := mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, "")

	// Reading tokens does not take the legacy state over.
	if got := tokenOf(store.Tokens("work", storeServerURL)); got != "legacy-token" {
		t.Fatalf("work tokens = %s", got)
	}
	if got := storedKeys(t, backend); !slices.Equal(got, []string{storeServerURL}) {
		t.Fatalf("stored keys = %q", got)
	}

	if got := loadedAccessToken(t, forServer(t, store, "my_work")); got == nil || *got != "legacy-token" {
		t.Fatalf("my_work token = %v", derefOr(got))
	}
	// Names differing only in `-` and `_` are the same server.
	if got := tokenOf(store.Tokens("my-work", storeServerURL)); got != "legacy-token" {
		t.Fatalf("my-work tokens = %s", got)
	}
	if got := loadedAccessToken(t, forServer(t, store, "personal")); got != nil {
		t.Fatalf("personal token = %v", *got)
	}
	if got, want := storedKeys(t, backend), []string{"mcp__my_work|" + storeServerURL}; !slices.Equal(got, want) {
		t.Fatalf("stored keys = %q, want %q", got, want)
	}
}

func TestMCPOAuthCredentialStoreSignsOutOfCredentialsStoredByServerURL(t *testing.T) {
	backend := &mcpext.InMemoryAuthStorageBackend{}
	seedLegacy(t, backend)
	store := mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, "")

	if !remove(t, store, "work") {
		t.Fatal("first remove = false")
	}
	if got := storedKeys(t, backend); len(got) != 0 {
		t.Fatalf("stored keys = %q", got)
	}
	if remove(t, store, "work") {
		t.Fatal("second remove = true")
	}
}

func derefOr(s *string) string {
	if s == nil {
		return "<undefined>"
	}
	return *s
}
