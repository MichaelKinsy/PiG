package mcpext

import (
	"net/url"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstreamHyperlink is hyperlink() of .upstream/v1.0.0/packages/tui/src/terminal-image.ts:700-702, an OSC 8 link.
func upstreamHyperlink(text, target string) string {
	return "\x1b]8;;" + target + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// Implementation-derived (Pi 1.0.0 adds no test for #10186): `/mcp login` shows the sign-in URL as a terminal link in
// the TUI, followed by a short link line that stays on one line when the URL wraps, and the plain URL in other modes.
// The browser opens either way (.upstream/v1.0.0/packages/coding-agent/src/extensions/mcp/index.ts:929-938).
func TestMcpLoginShowsTheSignInURLAsALinkInTheTerminalUI(t *testing.T) {
	const target = "https://auth.example/authorize?client_id=pi&state=abc"
	authorizationURL, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	label := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		label = "Cmd+click to open"
	}
	for _, tc := range []struct {
		mode extension.ExtensionMode
		want string
	}{
		{extension.ModeTUI, "Sign in to MCP server \"docs\" in your browser:\n" + upstreamHyperlink(target, target) + "\n" + upstreamHyperlink(label, target)},
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
