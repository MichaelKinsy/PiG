package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// index.ts loginCommand showAuthorizationUrl (1.0.1): the notice shows the plain URL in every mode it is used in; the
// terminal UI signs in on the manager's sign-in screen, which links the URL and copies it.
func TestSignInNoticeShowsThePlainAuthorizationURL(t *testing.T) {
	u, _ := nodeurl.ParseURL("https://as.example/authorize?client_id=c&state=s")
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
		ctx := CommandContext{Mode: c.mode, Notify: func(message, _ string) { got = append(got, message) }}
		(&commandSignIn{e: e, ctx: ctx, name: "docs"}).ShowAuthorizationURL(u)
		if len(got) != 1 || got[0] != c.want {
			t.Fatalf("mode %s: notes = %q, want %q", c.mode, got, c.want)
		}
		if opened != u.String() {
			t.Fatalf("mode %s: opened %q", c.mode, opened)
		}
	}
}
