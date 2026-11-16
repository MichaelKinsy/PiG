//go:build !pig_strip_node_extensions

package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi adapts a config.oauth login's legacy callbacks to the login interaction's prompt (provider-composer.ts:361-364
// adaptOAuth: onSelect is prompt({type: "select"})). Cancelling the selector rejects that prompt with "Login cancelled"
// (interactive-mode.ts:6243-6276 showAuthSelect), the flow rejects with it, and /login reopens the menu the login was
// started from instead of reporting a failure (interactive-mode.ts:6365-6366).
func TestConfigOAuthLoginSelectCancelReturnsToMenu(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			m.runCtx = t.Context()
			m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			dir := t.TempDir()
			var config subprocess.ExtConfig
			switch language {
			case "node":
				entry := filepath.Join(dir, "cfg-oauth.mjs")
				if err := os.WriteFile(entry, []byte(configOAuthCancelNodeFixture), 0o600); err != nil {
					t.Fatal(err)
				}
				config = subprocess.ExtConfig{Name: "cfg-oauth", Source: entry, Enabled: true}
			case "go":
				config = goFactoryFixtureConfig(t, dir, "cfg-oauth", "example.com/cfgoauth", configOAuthCancelGoFixture)
			}
			host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			host.SetProviderCallbacks(m.opts.ModelRegistry.RegisterExtensionProvider, m.opts.ModelRegistry.UnregisterProvider)
			if _, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{config}); len(errs) != 0 {
				t.Fatalf("load %s fixture: %v", language, errs)
			}
			// The services apply the registrations the extension queued while it loaded.
			host.Runtime().BindProviderActions(host.Runtime().BoundProviderActions(), nil)
			done := dispatchLogin(t, m, "Cfg SSO")
			waitForRender(t, m.editorContainer, "Select authentication method for Cfg SSO:")
			deliverModalInput(t, m, []byte("\r"))
			waitForRender(t, m.editorContainer, "Choose a team:")
			deliverModalInput(t, m, []byte("\x1b"))
			waitForRender(t, m.editorContainer, "Select authentication method for Cfg SSO:")
			if got := plainRender(m.chatContainer) + plainRender(m.editorContainer); strings.Contains(got, "failed") || strings.Contains(got, "Failed") {
				t.Fatalf("a cancelled select reported a failure:\n%s", got)
			}
			deliverModalInput(t, m, []byte("\x1b"))
			awaitLogin(t, done)
			if credential, ok := storedNativeCredential(t, m, "cfg-sso"); ok {
				t.Fatalf("a cancelled login stored %+v", credential)
			}
		})
	}
}
