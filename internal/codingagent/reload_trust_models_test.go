package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func reloadTrustMode(t *testing.T, autoTrustCwd string) (*InteractiveMode, string) {
	t.Helper()
	cwd, agentDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	m := statusBorderMode(t, false)
	m.opts.CWD, m.opts.AgentDir = cwd, agentDir
	m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	m.autoTrustOnReloadCwd = autoTrustCwd
	if autoTrustCwd == "cwd" {
		m.autoTrustOnReloadCwd = cwd
	}
	return m, cwd
}

func addProjectResource(t *testing.T, cwd string) {
	t.Helper()
	dir := filepath.Join(ProjectConfigDir(cwd), "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func reloadChatText(m *InteractiveMode) string {
	return widthx.StripAnsi(strings.Join(m.chatContainer.Render(200), "\n"))
}

// maybeSaveImplicitProjectTrustAfterReload (interactive-mode.ts:5260-5285): a
// start without trust-requiring resources that gains them, in a trusted project
// with no saved decision, saves trust once; every other state saves nothing.
func TestImplicitProjectTrustAfterReload(t *testing.T) {
	t.Run("saves once when resources appear", func(t *testing.T) {
		m, cwd := reloadTrustMode(t, "cwd")
		if m.maybeSaveImplicitProjectTrustAfterReload() {
			t.Fatal("saved trust before the project had trust-requiring resources")
		}
		addProjectResource(t, cwd)
		if !m.maybeSaveImplicitProjectTrustAfterReload() {
			t.Fatal("did not save trust after resources appeared")
		}
		decision, err := NewProjectTrustStore(m.opts.AgentDir).Get(cwd)
		if err != nil || decision == nil || !*decision {
			t.Fatalf("saved decision = %v, %v", decision, err)
		}
		if m.maybeSaveImplicitProjectTrustAfterReload() {
			t.Fatal("saved trust twice")
		}
	})
	t.Run("an existing decision wins and disarms", func(t *testing.T) {
		m, cwd := reloadTrustMode(t, "cwd")
		addProjectResource(t, cwd)
		if err := NewProjectTrustStore(m.opts.AgentDir).Set(cwd, new(false)); err != nil {
			t.Fatal(err)
		}
		if m.maybeSaveImplicitProjectTrustAfterReload() || m.autoTrustOnReloadCwd != "" {
			t.Fatal("an existing decision was overwritten or left armed")
		}
		if decision, _ := NewProjectTrustStore(m.opts.AgentDir).Get(cwd); decision == nil || *decision {
			t.Fatalf("decision = %v, want the saved false", decision)
		}
	})
	t.Run("not armed or untrusted saves nothing", func(t *testing.T) {
		m, cwd := reloadTrustMode(t, "")
		addProjectResource(t, cwd)
		if m.maybeSaveImplicitProjectTrustAfterReload() {
			t.Fatal("saved trust without an armed cwd")
		}
		m, cwd = reloadTrustMode(t, "cwd")
		addProjectResource(t, cwd)
		m.opts.SettingsManager.SetProjectTrusted(false)
		if m.maybeSaveImplicitProjectTrustAfterReload() {
			t.Fatal("saved trust for an untrusted project")
		}
	})
	t.Run("a store failure warns and stays armed", func(t *testing.T) {
		m, cwd := reloadTrustMode(t, "cwd")
		addProjectResource(t, cwd)
		if err := os.WriteFile(filepath.Join(m.opts.AgentDir, "trust.json"), []byte("{broken"), 0o600); err != nil {
			t.Fatal(err)
		}
		if m.maybeSaveImplicitProjectTrustAfterReload() {
			t.Fatal("reported a save that failed")
		}
		if text := reloadChatText(m); !strings.Contains(text, "Could not save project trust after reload: ") || m.autoTrustOnReloadCwd == "" {
			t.Fatalf("warning/armed state wrong: %q armed=%q", text, m.autoTrustOnReloadCwd)
		}
	})
}

// handleReloadCommand's status names the saved trust (interactive-mode.ts:6468-6472).
func TestReloadStatusNamesSavedProjectTrust(t *testing.T) {
	for _, saved := range []bool{false, true} {
		sc, out := newFakeSlashCtx()
		sc.Reload = func() error { return nil }
		sc.ReloadSavedProjectTrust = func() bool { return saved }
		if err := reloadHandler(sc); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "context files; saved project trust"); got != saved {
			t.Fatalf("saved=%t status %q", saved, out.String())
		}
	}
}

// init and handleReloadCommand show a models.json load error (interactive-mode.ts:1203-1206, 6464-6467).
func TestModelsJSONErrorIsShown(t *testing.T) {
	m := statusBorderMode(t, false)
	agentDir := t.TempDir()
	m.opts.ModelRegistry = NewModelRegistry(agentDir)
	m.showModelsJSONError()
	if reloadChatText(m) != "" {
		t.Fatalf("a clean registry showed %q", reloadChatText(m))
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.ModelRegistry = NewModelRegistry(agentDir)
	m.showModelsJSONError()
	if text := reloadChatText(m); !strings.Contains(text, "Error: models.json error: Failed to parse models.json") {
		t.Fatalf("transcript = %q", text)
	}
}

// init (interactive-mode.ts:1189-1211) shows startup diagnostics, the migrated
// credentials warning, the models.json error, then the model fallback message.
func TestInitNoticesFollowUpstreamOrder(t *testing.T) {
	m := statusBorderMode(t, false)
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.ModelRegistry = NewModelRegistry(agentDir)
	m.opts.StartupDiagnostics = []AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "DIAGNOSTIC"}}
	m.opts.MigratedProviders = []string{"anthropic", "openai"}
	m.opts.ModelFallbackMessage = "FALLBACK"
	m.showInitNotices()
	text := reloadChatText(m)
	order := []string{"DIAGNOSTIC", "Migrated credentials to auth.json: anthropic, openai", "models.json error:", "FALLBACK"}
	last := -1
	for _, want := range order {
		at := strings.Index(text, want)
		if at <= last {
			t.Fatalf("%q missing or out of order in %q", want, text)
		}
		last = at
	}
}
