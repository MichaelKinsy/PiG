package mcpext_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// provider.ts:106 Date.now() + expires_in * 1000 is Infinity for an expires_in near Number.MAX_VALUE, and oauth.ts writes the state with
// JSON.stringify, which writes null: the sign-in saves. Read back, null - REFRESH_SKEW_MS <= Date.now() (oauth.ts:357), so the token is expired.
func TestMCPOAuthCredentialStoreSavesATokenExpiryThatOverflowsAsNull(t *testing.T) {
	backend := &mcpext.InMemoryAuthStorageBackend{}
	store := mcpext.NewMcpOAuthCredentialStoreWithBackend(backend, "")
	provider, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
		ServerURL: storeServerURL, RedirectURL: "http://127.0.0.1/callback", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "test"},
		Store: forServer(t, store, "work"), OnRedirect: func(context.Context, oauth.URL) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	expiresIn := 1e306
	if err := provider.SaveTokens(t.Context(), oauth.OAuthTokens{AccessToken: "a", TokenType: "Bearer", ExpiresIn: &expiresIn}); err != nil {
		t.Fatalf("SaveTokens = %v, want the state saved as JSON.stringify writes it", err)
	}
	var content string
	if err := backend.WithLock(func(current string, _ bool) (*string, error) { content = current; return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `"tokensExpireAt": null`) {
		t.Fatalf("stored = %s, want tokensExpireAt null", content)
	}
	state, err := forServer(t, store, "work").Load(t.Context())
	if err != nil || state == nil || state.TokensExpireAt == nil || *state.TokensExpireAt != 0 || state.Tokens == nil || state.Tokens.AccessToken != "a" {
		t.Fatalf("loaded %+v (%v), want the tokens with tokensExpireAt 0", state, err)
	}
}
