package mcpext_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// oauth.ts token(): `expired = state.tokensExpireAt !== undefined && state.tokensExpireAt - REFRESH_SKEW_MS <= Date.now()`, and only an expired token
// with a refresh token is refreshed before the request; a failed refresh falls through to the stored token. Every refresh runs under the store's
// refresh lock, so the lock's calls count the refreshes.
type lockCountingStore struct {
	mcpext.McpOAuthServerStore
	locks int
}

func (s *lockCountingStore) WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error {
	s.locks++
	return s.McpOAuthServerStore.WithRefreshLock(ctx, fn)
}

func TestTokenRefreshesAheadOfExpiryOnlyInsideTheSkew(t *testing.T) {
	lockDir, err := os.MkdirTemp("", "pi-mcp-expiry-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(lockDir) })
	now := float64(time.Now().UnixMilli())
	for _, tc := range []struct {
		name     string
		expireAt *float64
		refresh  string
		want     int
	}{
		{"inside the 30 s skew", new(now + 10_000), "r", 1},
		{"already expired", new(now - 1_000), "r", 1},
		{"outside the skew", new(now + 120_000), "r", 0},
		{"no expiry", nil, "r", 0},
		{"no refresh token", new(now - 1_000), "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, lockDir).ForServer("test", "https://mcp.example/mcp")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(t.Context(), oauth.McpOAuthState{Tokens: &oauth.OAuthTokens{AccessToken: "old", TokenType: "Bearer", RefreshToken: tc.refresh}, TokensExpireAt: tc.expireAt}); err != nil {
				t.Fatal(err)
			}
			counting := &lockCountingStore{McpOAuthServerStore: store}
			provider := mcpext.NewMcpAuthProvider(mcpext.McpAuthProviderOptions{
				ServerURL: "https://mcp.example/mcp", Store: counting,
				Settings: func() (mcpext.McpOAuthSettings, error) {
					return mcpext.McpOAuthSettings{}, errors.New("no refresh in this test")
				},
				OnChallenge: func(oauth.OAuthChallenge) {},
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			token, err := provider.Token(ctx)
			if err != nil || token != "old" {
				t.Fatalf("Token = %q, %v, want the stored token after a failed or skipped refresh", token, err)
			}
			if counting.locks != tc.want {
				t.Fatalf("refreshes = %d, want %d", counting.locks, tc.want)
			}
		})
	}
}
