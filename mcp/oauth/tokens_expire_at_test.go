package oauth_test

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// provider.ts:106 stores tokensExpireAt = Date.now() + expires_in * 1000, a JavaScript number: fractional when expires_in is, and far in the future when it is
// huge. The persisted state (types.ts McpOAuthState.tokensExpireAt?: number) is shared with Pi, so a fractional value Pi wrote must still read.
func TestSaveTokensKeepsExpiresInAsJavaScriptArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name      string
		expiresIn float64
		check     func(expireAt, before, after float64) bool
	}{
		{"fractional milliseconds", 0.0005, func(at, before, after float64) bool {
			return at >= before+0.5 && at <= after+0.5 && at != math.Trunc(at)
		}},
		{"beyond int64", 1e300, func(at, _, _ float64) bool { return at == 1e303 }},
		{"negative", -10, func(at, before, after float64) bool { return at >= before-10_000 && at <= after-10_000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &oauth.MemoryOAuthStateStore{}
			provider := newProviderForStore(t, store)
			before := float64(time.Now().UnixMilli())
			if err := provider.SaveTokens(t.Context(), oauth.OAuthTokens{AccessToken: "a", TokenType: "Bearer", ExpiresIn: &tc.expiresIn}); err != nil {
				t.Fatal(err)
			}
			after := float64(time.Now().UnixMilli())
			state, _ := store.Load(t.Context())
			if state == nil || state.TokensExpireAt == nil || !tc.check(float64(*state.TokensExpireAt), before, after) {
				t.Fatalf("tokensExpireAt = %v, want Date.now() + %v * 1000", state.TokensExpireAt, tc.expiresIn)
			}
		})
	}
}

func TestPersistedStateReadsAFractionalTokensExpireAt(t *testing.T) {
	var state oauth.McpOAuthState
	if err := json.Unmarshal([]byte(`{"tokensExpireAt":1760000000000.5}`), &state); err != nil {
		t.Fatalf("a state Pi wrote with a fractional tokensExpireAt does not read: %v", err)
	}
	if state.TokensExpireAt == nil || float64(*state.TokensExpireAt) != 1760000000000.5 {
		t.Fatalf("tokensExpireAt = %v", state.TokensExpireAt)
	}
	if out, _ := json.Marshal(state); !strings.Contains(string(out), `"tokensExpireAt":1760000000000.5`) {
		t.Fatalf("state = %s", out)
	}
}

func newProviderForStore(t *testing.T, store oauth.McpOAuthStateStore) *oauth.McpOAuthProvider {
	t.Helper()
	provider, err := oauth.NewMcpOAuthProvider(oauth.McpOAuthProviderOptions{
		ServerURL: "https://one.example/mcp", RedirectURL: "http://127.0.0.1/callback", ClientMetadata: oauth.OAuthClientMetadata{ClientName: "test"},
		Store: store, OnRedirect: func(context.Context, *url.URL) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
