package oauth_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

func tokenRequestTo(t *testing.T, status int, body string, resource string) (url.Values, error) {
	t.Helper()
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	_, err := oauth.ExchangeAuthorizationCode(t.Context(), server.URL, oauth.ExchangeAuthorizationCodeOptions{
		TokenRequestOptions: oauth.TokenRequestOptions{
			Metadata:          &oauth.AuthorizationServerMetadata{TokenEndpoint: server.URL + "/token"},
			ClientInformation: oauth.OAuthClientInformationMixed{ClientID: "id"},
			Resource:          resource,
		},
		Code: "c", CodeVerifier: "v", RedirectURL: "http://localhost/callback",
	})
	return form, err
}

// flow.ts:209 sends `resource` with a token request only when the option is set.
// Pi: packages/mcp/src/oauth/flow.ts:209
func TestTokenRequestSendsTheResourceParameterOnlyWhenSet(t *testing.T) {
	form, _ := tokenRequestTo(t, 200, `{"access_token":"a","token_type":"Bearer"}`, "https://mcp.example.com/")
	if got := form["resource"]; len(got) != 1 || got[0] != "https://mcp.example.com/" {
		t.Errorf("resource parameter = %q, want the option", got)
	}
	form, _ = tokenRequestTo(t, 200, `{"access_token":"a","token_type":"Bearer"}`, "")
	if _, present := form["resource"]; present {
		t.Errorf("resource parameter present without the option: %q", form["resource"])
	}
}

// flow.ts:229-242: an error body is an OAuth error whatever the status when `error` is a string (description and uri only when strings, the
// description defaulting to the code); any other failing response is server_error `HTTP <status>: <text>`; a successful one is parsed as tokens.
// Pi: packages/mcp/src/oauth/flow.ts:229-242
func TestTokenResponseErrorsFollowPisBodyBeforeStatusRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		code    string
		message string
		uri     string
	}{
		{"all fields", 400, `{"error":"invalid_grant","error_description":"expired","error_uri":"https://e/x"}`, "invalid_grant", "expired", "https://e/x"},
		{"error on a 200", 200, `{"error":"invalid_grant","error_description":"expired"}`, "invalid_grant", "expired", ""},
		{"description defaults to the code", 400, `{"error":"invalid_grant"}`, "invalid_grant", "invalid_grant", ""},
		{"non-string description and uri are ignored", 400, `{"error":"x","error_description":5,"error_uri":false}`, "x", "x", ""},
		{"non-string error falls to the status", 400, `{"error":5}`, "server_error", `HTTP 400: {"error":5}`, ""},
		{"JSON null body with a failing status", 500, `null`, "server_error", "HTTP 500: null", ""},
		{"array body with a failing status", 502, `[{"error":"x"}]`, "server_error", `HTTP 502: [{"error":"x"}]`, ""},
		{"non-JSON body with a failing status", 502, `<html>bad gateway</html>`, "server_error", "HTTP 502: <html>bad gateway</html>", ""},
	} {
		_, err := tokenRequestTo(t, tc.status, tc.body, "")
		var oauthErr *oauth.OAuthError
		if !errors.As(err, &oauthErr) {
			t.Errorf("%s: error = %v, want an OAuthError", tc.name, err)
			continue
		}
		if oauthErr.Code != tc.code || oauthErr.Error() != tc.message || oauthErr.ErrorURI != tc.uri {
			t.Errorf("%s: OAuthError{code %q, message %q, uri %q}, want {%q, %q, %q}", tc.name, oauthErr.Code, oauthErr.Error(), oauthErr.ErrorURI, tc.code, tc.message, tc.uri)
		}
	}
}
