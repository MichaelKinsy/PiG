package mcpext

import (
	"net/url"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
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
	for _, c := range cases {
		response, err := responseFromRedirectURL("http://127.0.0.1:1/callback?"+c.query, "s")
		if err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if response.code != "c" || (response.iss == nil) != (c.iss == nil) || (c.iss != nil && *response.iss != *c.iss) {
			t.Fatalf("%s: response = %+v", c.query, response)
		}
	}
}

// index.ts loginCommand showAuthorizationUrl: in the terminal UI the URL and a short line both link to it, so a
// wrapped URL stays openable; other modes print the URL alone.
func TestSignInShowsTheAuthorizationURLAsLinksInTheTerminalUI(t *testing.T) {
	u, _ := url.Parse("https://as.example/authorize?client_id=c&state=s")
	open := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		open = "Cmd+click to open"
	}
	cases := []struct {
		mode extension.ExtensionMode
		want string
	}{
		{extension.ModeTUI, "Sign in to MCP server \"docs\" in your browser:\n" + tui.Hyperlink(u.String(), u.String()) + "\n" + tui.Hyperlink(open, u.String())},
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
