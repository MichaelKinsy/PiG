package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/web"

	// The web facet nothing else loads on the server: the app layout.
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
)

// Pi's own web client (the vendored bundle, unchanged) in headless Chromium against our server: `/` loads as the loopback
// owner and a faux-provider chat round trip completes (ui/ui.mjs). Needs Node with playwright-core and Chromium
// (P2_UI_NODE_PATH names the node_modules holding playwright-core; PLAYWRIGHT_BROWSERS_PATH the browsers): skipped without.
func TestWebClientRoundTrip(t *testing.T) {
	modules := os.Getenv("P2_UI_NODE_PATH")
	if modules == "" {
		t.Skip("P2_UI_NODE_PATH (node_modules with playwright-core) is not set")
	}
	enabled := true
	// The new-session screen lists only signed-in providers' models; the fake provider is chosen as the default, as Pi's
	// ui-* tests do through Models.setDefault (the settings file is what that writes).
	dataDir := t.TempDir()
	settings := `{"defaultModel":{"provider":"faux","modelId":"faux-1"},"thinkingLevel":"off"}`
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Web: &enabled, DataDir: dataDir})
	bundle, err := web.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", "ui/ui.mjs", "http://127.0.0.1:"+strconv.Itoa(fixture.Server.Port), strconv.Itoa(os.Getpid()))
	command.Env = append(os.Environ(), "P2_UI_TITLE="+bundle.Brand.Name)
	output, err := command.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
}

// Ports the in-browser case of test/p2/web.test.ts: the app runs under its CSP, and another origin cannot frame it
// (ui/frame.mjs). The headers themselves are hosts/web_test.go's.
func TestWebClientRunsUnderItsCSPAndCannotBeFramed(t *testing.T) {
	modules := os.Getenv("P2_UI_NODE_PATH")
	if modules == "" {
		t.Skip("P2_UI_NODE_PATH (node_modules with playwright-core) is not set")
	}
	enabled := true
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Web: &enabled})
	output, err := exec.Command("node", "ui/frame.mjs", "http://127.0.0.1:"+strconv.Itoa(fixture.Server.Port)).CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
}
