package codingagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 1.5.0 Models.resolveRefreshCredential refreshes Radius through refreshStoredOAuthCredential (resolve.ts:119-155): the operation's signal cancels the caller and the wait for the lock, but the refresh itself gets AbortSignal.timeout(15 s), so a token request that has started is not cut (it may already have spent the refresh token) and the caller returns at once.
func TestRadiusExpiredOAuthRefreshReturnsToACancelledCallerWithoutCuttingTheTokenRequest(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		name := "request_auth"
		if catalog {
			name = "catalog"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/oauth/token" {
					t.Errorf("unexpected request %s", r.URL.Path)
					return
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("read token form: %v", err)
				}
				close(started)
				select {
				case <-r.Context().Done():
					close(cancelled)
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			registry, auth, _ := radiusTestRegistry(t, `{"providers":{"radius-dev":{"baseUrl":"`+server.URL+`","oauth":"radius"}}}`, map[string]ai.Credential{
				"radius-dev": {Type: ai.CredentialOAuth, Access: "old", Refresh: "old-refresh", Expires: 1},
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				if catalog {
					result := registry.RefreshCatalogs(ctx, CatalogRefreshOptions{AllowNetwork: true, Providers: []string{"radius-dev"}})
					if !result.Aborted || len(result.Errors) != 0 {
						t.Errorf("cancelled refresh = %+v", result)
					}
				} else if key, err := registry.RadiusAPIKey(ctx, "radius-dev"); key != "" || err == nil {
					t.Errorf("cancelled request auth = %q, %v", key, err)
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("token request did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("the cancelled caller did not return")
			}
			select {
			case <-cancelled:
				t.Fatal("the operation's cancellation cut the token request that had started")
			case <-time.After(100 * time.Millisecond):
			}
			stored, _, err := auth.GetRaw("radius-dev")
			if err != nil || stored.Access != "old" || stored.Refresh != "old-refresh" {
				t.Fatalf("cancelled refresh changed credential: %+v, %v", stored, err)
			}
		})
	}
}
