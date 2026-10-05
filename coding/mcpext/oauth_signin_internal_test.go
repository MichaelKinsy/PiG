package mcpext

import (
	"net/url"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// oauth.ts responseFromRedirectURL: a pasted redirect URL carries its `iss` parameter to the code exchange, and
// `searchParams.get("iss") ?? undefined` keeps a present but empty one, which the exchange then checks.
func TestResponseFromRedirectURLKeepsTheIssParameter(t *testing.T) {
	cases := []struct {
		query string
		iss   *string
	}{
		{"code=c&state=s", nil},
		{"code=c&state=s&iss=https%3A%2F%2Fas.example", new("https://as.example")},
		{"code=c&state=s&iss=", new("")},
	}
	redirect, _ := url.Parse("http://127.0.0.1:1/callback")
	for _, c := range cases {
		response, err := responseFromRedirectURL("http://127.0.0.1:1/callback?"+c.query, "s", redirect)
		if err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if response.code != "c" || (response.iss == nil) != (c.iss == nil) || (c.iss != nil && *response.iss != *c.iss) {
			t.Fatalf("%s: response = %+v", c.query, response)
		}
	}
}

// index.ts loginCommand showAuthorizationUrl (1.0.1): the notice shows the plain URL in every mode it is used in; the
// terminal UI signs in on the manager's sign-in screen, which links the URL and copies it.
func TestSignInNoticeShowsThePlainAuthorizationURL(t *testing.T) {
	u, _ := url.Parse("https://as.example/authorize?client_id=c&state=s")
	cases := []struct {
		mode extension.ExtensionMode
		want string
	}{
		{extension.ModeTUI, "Sign in to MCP server \"docs\" in your browser:\n" + u.String()},
		{extension.ModeRPC, "Sign in to MCP server \"docs\" in your browser:\n" + u.String()},
	}
	for _, c := range cases {
		var got []string
		opened := ""
		e := &Extension{options: Options{OpenURL: func(u string) { opened = u }}}
		ctx := CommandContext{Mode: c.mode, EventContext: EventContext{Notify: func(message, _ string) { got = append(got, message) }}}
		(&commandSignIn{e: e, ctx: ctx, name: "docs"}).ShowAuthorizationURL(u)
		if len(got) != 1 || got[0] != c.want {
			t.Fatalf("mode %s: notes = %q, want %q", c.mode, got, c.want)
		}
		if opened != u.String() {
			t.Fatalf("mode %s: opened %q", c.mode, opened)
		}
	}
}
