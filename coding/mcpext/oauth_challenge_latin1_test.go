package mcpext_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// oauth.ts parses response.headers.get("www-authenticate"), which Node's fetch decodes as Latin-1: a lone 0xA0 byte is U+00A0, JavaScript
// whitespace, so it separates the scope field of the challenge.
func TestMcpAuthProviderParsesTheChallengeHeaderAsLatin1(t *testing.T) {
	serverURL := "https://mcp.example.com/mcp"
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "").ForServer("test", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	var seen oauth.OAuthChallenge
	provider := mcpext.NewMcpAuthProvider(mcpext.McpAuthProviderOptions{ServerURL: serverURL, Store: store, OnChallenge: func(c oauth.OAuthChallenge) { seen = c }})
	mcpURL, _ := url.Parse(serverURL)
	err = provider.(mcp.UnauthorizedHandler).OnUnauthorized(t.Context(), mcp.UnauthorizedContext{
		Response:  &http.Response{StatusCode: 403, Header: http.Header{"Www-Authenticate": {"Bearer error=\"insufficient_scope\"\xa0scope=\"a b\""}}},
		ServerURL: mcpURL, Fetch: http.DefaultClient, Token: "old",
	})
	if _, ok := errors.AsType[*oauth.McpOAuthAuthorizationRequiredError](err); !ok {
		t.Fatalf("err = %v, want a new sign-in for more scope", err)
	}
	if seen.Error != "insufficient_scope" || seen.Scope != "a b" {
		t.Fatalf("challenge = %+v, want error insufficient_scope and scope \"a b\"", seen)
	}
}
