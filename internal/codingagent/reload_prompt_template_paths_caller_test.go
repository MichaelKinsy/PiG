package codingagent_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// promptConflicts returns the [Prompt conflicts] section of the harness's resource listing, without ANSI or trailing padding. The listing wraps
// a path longer than the terminal, as Pi's does, and a t.TempDir path's length varies with TMPDIR and its random suffixes (about 120 columns
// under the gate's TMPDIR, where 120 columns broke "nosuch.md" across two lines), so the section is rendered wider than any such path.
func promptConflicts(t *testing.T, h *icodingagent.TestHarness) string {
	t.Helper()
	lines := strings.Split(widthx.StripAnsi(h.LoadedResourcesAt(4096)), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	listing := strings.Join(lines, "\n")
	_, conflicts, ok := strings.Cut(listing, "[Prompt conflicts]\n")
	if !ok {
		t.Fatalf("listing has no [Prompt conflicts]:\n%s", listing)
	}
	return conflicts
}

// resource-loader.ts reload (:615-626) runs on the production /reload: a --prompt-template path that does not exist is listed under
// [Prompt conflicts] with the error "Prompt template path does not exist".
func TestSlashReloadReportsAMissingPromptTemplatePath(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	missing := filepath.Join(cwd, "nosuch.md")
	h := reloadCallerPair(t, cwd, agentDir, "", nil, func(opts *icodingagent.InteractiveModeOptions) {
		opts.CLIPromptTemplatePaths = []string{missing}
	})
	h.Do(func() { h.Enter("/reload") })
	if conflicts := promptConflicts(t, h); !strings.Contains(conflicts, filepath.Base(missing)+"\n    Prompt template path does not exist") {
		t.Fatalf("reloaded [Prompt conflicts] lacks the missing --prompt-template path:\n%s", conflicts)
	}
}

// A replacement Session's cwd-bound build reloads its resources as resource-loader.ts reload does (:615-626), so the rebound listing
// reports a --prompt-template path that does not exist.
func TestRebindReportsAMissingPromptTemplatePath(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	h := reloadCallerPair(t, cwd, agentDir, "", nil)
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner(nil, services.CWD())
	manager, err := coding.NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "faux-1", Provider: &scriptedProvider{}}, Runner: runner, SessionManager: manager, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	missing := filepath.Join(cwd, "nosuch.md")
	replacement := icodingagent.InteractiveReplacement{
		CWD: services.CWD(), Settings: services.SettingsManager().Get(), SettingsManager: services.SettingsManager(),
		ModelRegistry: services.Registry().ModelRegistry, ExtensionRunner: runner,
		SessionStartEvent:      extension.SessionStartEvent{Type: "session_start", Reason: "new"},
		CLIPromptTemplatePaths: []string{missing},
	}
	if err := h.RebindToReplacement(t.Context(), session, replacement); err != nil {
		t.Fatal(err)
	}
	if conflicts := promptConflicts(t, h); !strings.Contains(conflicts, filepath.Base(missing)+"\n    Prompt template path does not exist") {
		t.Fatalf("rebound [Prompt conflicts] lacks the missing --prompt-template path:\n%s", conflicts)
	}
}
