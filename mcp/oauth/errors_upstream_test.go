package oauth

import (
	"errors"
	"testing"
)

// Pi mcp/src/oauth/errors.ts OAuthError: message falls back to the code, name is "OAuthError", errorUri is optional.
func TestNewOAuthErrorUpstream(t *testing.T) {
	e := NewOAuthError("invalid_grant", "", "https://example.test/err")
	if e.Error() != "invalid_grant" || e.Name() != "OAuthError" || e.Code != "invalid_grant" || e.ErrorURI != "https://example.test/err" {
		t.Fatalf("got %+v / %q", e, e.Error())
	}
	e = NewOAuthError("server_error", "boom")
	if e.Error() != "boom" || e.ErrorURI != "" {
		t.Fatalf("got %+v", e)
	}
	var target *OAuthError
	if !errors.As(error(e), &target) || target != e {
		t.Fatal("errors.As does not find the OAuthError")
	}
}
