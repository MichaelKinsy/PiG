package mcpext

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// renderPage of the MCP callback server (extensions/mcp/oauth.ts listenForCallback) against pinned Pi: an ok page renders the success page with the sign-in message, every
// other page renders the error page with the page's message and its details (absent, empty or set, with markup that must be escaped), byte for byte.
func TestMCPCallbackRenderPageMatchesPi(t *testing.T) {
	pages := []struct {
		page oauth.OAuthCallbackPage
		json string
	}{
		{oauth.OAuthCallbackPage{OK: true}, `{"ok":true}`},
		{oauth.OAuthCallbackPage{OK: true, Message: "ignored for an ok page", Details: "ignored"}, `{"ok":true,"message":"ignored for an ok page","details":"ignored"}`},
		{oauth.OAuthCallbackPage{OK: false, Message: "Authorization failed."}, `{"ok":false,"message":"Authorization failed."}`},
		{oauth.OAuthCallbackPage{OK: false, Message: "Authorization failed.", Details: "access_denied"}, `{"ok":false,"message":"Authorization failed.","details":"access_denied"}`},
		{oauth.OAuthCallbackPage{OK: false, Message: `<b>"x"</b> & 'y'`, Details: `<script>alert(1)</script>`}, `{"ok":false,"message":"<b>\"x\"</b> & 'y'","details":"<script>alert(1)</script>"}`},
		{oauth.OAuthCallbackPage{OK: false, Message: "日本語 \u2028 line", Details: "d\nline two"}, `{"ok":false,"message":"日本語 \u2028 line","details":"d\nline two"}`},
		{oauth.OAuthCallbackPage{OK: false, Message: ""}, `{"ok":false,"message":""}`},
	}
	var probes []json.RawMessage
	for _, p := range pages {
		probes = append(probes, json.RawMessage(p.json))
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/oauth_callback_page.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, p := range pages {
		if got := renderCallbackPage(p.page); got != want[i] {
			t.Errorf("page %d (%s): Pig page differs from Pi's\n  Pig %q\n  Pi  %q", i, p.json, got, want[i])
		}
	}
}
