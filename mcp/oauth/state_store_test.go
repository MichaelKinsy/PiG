package oauth_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// PiG-only: packages/mcp/test/oauth.test.ts uses MemoryOAuthStateStore as a backing store but never tests its own
// contract: load() is undefined until a save, and both directions copy (structuredClone) so a caller's later mutation
// never reaches the stored state.
func TestMemoryOAuthStateStoreLoadsNothingUntilASaveAndCopiesBothWays(t *testing.T) {
	ctx := t.Context()
	store := &oauth.MemoryOAuthStateStore{}
	if got, err := store.Load(ctx); err != nil || got != nil {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	saved := oauth.McpOAuthState{ServerURL: "https://s.example/mcp", Tokens: &oauth.OAuthTokens{AccessToken: "a", TokenType: "Bearer"}, CodeVerifier: "v"}
	if err := store.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	saved.Tokens.AccessToken = "mutated-after-save"
	saved.CodeVerifier = "mutated"
	first, err := store.Load(ctx)
	if err != nil || first == nil || first.Tokens == nil || first.Tokens.AccessToken != "a" || first.CodeVerifier != "v" || first.ServerURL != "https://s.example/mcp" {
		t.Fatalf("load = %+v, %v", first, err)
	}
	first.Tokens.AccessToken = "mutated-after-load"
	second, _ := store.Load(ctx)
	if second.Tokens.AccessToken != "a" {
		t.Fatalf("a mutation of a loaded copy reached the store: %q", second.Tokens.AccessToken)
	}
}
