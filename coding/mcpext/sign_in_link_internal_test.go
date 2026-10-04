package mcpext

import (
	"net/url"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 1.0.1 (.upstream/v1.0.1/packages/coding-agent/src/extensions/mcp/index.ts loginCommand): outside the terminal UI,
// `/mcp login` shows the plain sign-in URL in a notice and opens the browser. The terminal UI signs in on the manager's
// sign-in screen instead (TestMcpLoginSignsInOnTheManagerSignInScreenInTheTerminalUI), so the notice carries no
// terminal link in any mode.
func TestMcpLoginNoticeShowsThePlainSignInURL(t *testing.T) {
	const target = "https://auth.example/authorize?client_id=pi&state=abc"
	authorizationURL, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode extension.ExtensionMode
		want string
	}{
		{extension.ModeTUI, "Sign in to MCP server \"docs\" in your browser:\n" + target},
		{extension.ModePrint, "Sign in to MCP server \"docs\" in your browser:\n" + target},
		{extension.ModeRPC, "Sign in to MCP server \"docs\" in your browser:\n" + target},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			var notes, opened []string
			signIn := &commandSignIn{
				e: &Extension{options: Options{OpenURL: func(u string) { opened = append(opened, u) }}},
				ctx: CommandContext{
					EventContext: EventContext{Notify: func(message, level string) { notes = append(notes, level+": "+message) }},
					Mode:         tc.mode,
					HasUI:        tc.mode != extension.ModePrint,
				},
				name: "docs",
			}
			signIn.ShowAuthorizationURL(authorizationURL)
			if want := []string{"info: " + tc.want}; !slices.Equal(notes, want) {
				t.Errorf("notifications = %q, want %q", notes, want)
			}
			if !slices.Equal(opened, []string{target}) {
				t.Errorf("opened = %q, want %q", opened, []string{target})
			}
		})
	}
}
