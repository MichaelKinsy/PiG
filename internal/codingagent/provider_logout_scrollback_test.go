package codingagent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// /logout of a provider changes no transcript row, and Pi asks for no clearing redraw after it (interactive-mode.ts:6141-6150 showOAuthSelector: the logout, then showStatus, which
// requests an ordinary render), so the terminal's scrollback stays.
func TestLogoutKeepsTheTerminalScrollback(t *testing.T) {
	m, terminal := scrollbackMode(t)
	m.opts.AgentDir = t.TempDir()
	store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	if err := m.runOAuthLogout(context.Background(), "anthropic"); err != nil {
		t.Fatal(err)
	}
	if _, stored, _ := store.Get("anthropic"); stored {
		t.Fatal("the logout left the credential stored")
	}
	m.tuiInst.Render()
	requireDifferentialFrame(t, terminal.String(), "")
}
