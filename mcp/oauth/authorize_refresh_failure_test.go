package oauth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

type invalidationRecorder struct {
	*testOAuthProvider
	kinds []string
}

func (p *invalidationRecorder) InvalidateCredentials(ctx context.Context, kind string) error {
	p.kinds = append(p.kinds, kind)
	return p.testOAuthProvider.InvalidateCredentials(ctx, kind)
}

// What authorizeMcp does when the refresh grant fails (Pi packages/mcp/src/oauth/flow.ts):
//   - runFlow (flow.ts:399-409) falls back to a new authorization (REDIRECT) only when the refresh failed with a non-OAuth error or an OAuthError whose code is
//     "server_error"; any other OAuthError is rethrown;
//   - authorizeMcp (flow.ts:430-440) then invalidates "all" credentials for invalid_client / unauthorized_client and "tokens" for invalid_grant and
//     runs the flow once more; any other error passes through without invalidating anything.
//
// Pi: packages/mcp/src/oauth/flow.ts:399-409 (runFlow), :430-440 (authorizeMcp)
func TestAuthorizeMcpHandlesAFailedRefreshLikePi(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		wantRedirect bool
		wantErrCode  string
		wantKinds    []string
	}{
		{"server_error falls back to a new authorization", 500, `oops`, true, "", nil},
		{"other OAuth errors are rethrown untouched", 400, `{"error":"invalid_scope"}`, false, "invalid_scope", nil},
		{"invalid_grant drops the tokens and starts over", 400, `{"error":"invalid_grant"}`, true, "", []string{"tokens"}},
		{"invalid_client drops everything and runs the flow again", 401, `{"error":"invalid_client"}`, false, "", []string{"all"}},
		{"unauthorized_client drops everything too", 400, `{"error":"unauthorized_client"}`, false, "", []string{"all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var origin string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			origin = server.URL
			base := newTestOAuthProvider("http://localhost/callback")
			base.client = &oauth.OAuthClientInformationMixed{ClientID: "client"}
			base.tokenSet = &oauth.OAuthTokens{AccessToken: "old", TokenType: "Bearer", RefreshToken: "refresh"}
			base.discovery = &oauth.OAuthDiscoveryState{
				AuthorizationServerURL: origin,
				AuthorizationServerMetadata: &oauth.AuthorizationServerMetadata{
					Issuer: origin, AuthorizationEndpoint: origin + "/authorize", TokenEndpoint: origin + "/token",
					ResponseTypesSupported: []string{"code"}, CodeChallengeMethodsSupported: []string{"S256"},
				},
			}
			provider := &invalidationRecorder{testOAuthProvider: base}
			result, err := oauth.AuthorizeMcp(t.Context(), provider, oauth.OAuthFlowOptions{ServerURL: origin + "/mcp"})
			if !slices.Equal(provider.kinds, tc.wantKinds) {
				t.Errorf("invalidated %q, want %q", provider.kinds, tc.wantKinds)
			}
			if tc.wantRedirect {
				if err != nil || result != oauth.OAuthRedirect || base.authorizationURL == nil {
					t.Errorf("result %q, error %v, redirect %v; want a REDIRECT", result, err, base.authorizationURL)
				}
				return
			}
			if err == nil || base.authorizationURL != nil {
				t.Fatalf("result %q, error %v, redirect %v; want an error and no redirect", result, err, base.authorizationURL)
			}
			if tc.wantErrCode != "" {
				var oauthErr *oauth.OAuthError
				if !errors.As(err, &oauthErr) || oauthErr.Code != tc.wantErrCode {
					t.Errorf("error = %v, want OAuthError %s", err, tc.wantErrCode)
				}
			}
		})
	}
}
