package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type startupPickResult struct {
	path     string
	selected bool
	err      error
}

// runStartupSessionPicker starts selectStartupSession over one persisted session and a fake terminal, and returns the terminal, the frames and the result channel. The agent dir holds the user's keybindings.json, as Pi's KeybindingsManager.create() reads it (session-picker.ts:22).
func runStartupSessionPicker(t *testing.T, agentDir, cwd string) (*fakeStartupTerminal, *startupPickerOutput, <-chan startupPickResult) {
	t.Helper()
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	manager := NewSessionManager(cwd)
	out := &startupPickerOutput{}
	terminal := &fakeStartupTerminal{}
	done := make(chan startupPickResult, 1)
	go func() {
		path, selected, err := selectStartupSession(
			func(options SessionListOptions) ([]SessionInfo, error) { return manager.ListCurrentSessions(options) },
			func(options SessionListOptions) ([]SessionInfo, error) { return manager.ListAllSessions(options) },
			StartupUIOptions{AgentDir: agentDir, Settings: Settings{Theme: "dark"}},
			tui.NewWithOutput(out, 120, 30), terminal)
		done <- startupPickResult{path, selected, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stripANSI(out.String()), "only session") {
		if time.Now().After(deadline) {
			t.Fatalf("the picker never listed the session; frames %q", stripANSI(out.String()))
		}
		time.Sleep(time.Millisecond)
	}
	return terminal, out, done
}

func pickerResult(t *testing.T, done <-chan startupPickResult, why string) startupPickResult {
	t.Helper()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result
	case <-time.After(10 * time.Second):
		t.Fatalf("the picker did not finish: %s", why)
		return startupPickResult{}
	}
}

// Pi cli/session-picker.ts:25-36: the selector's select callback resolves the chosen session's path, once.
// mutation-checked: returning no path, or the wrong path, from selectStartupSession fails it
func TestSelectStartupSessionResolvesTheSelectedPath(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, t.TempDir())
	cwd := t.TempDir()
	path := listingPersistedSession(t, defaultSessionDir(cwd), cwd, "only session")
	terminal, _, done := runStartupSessionPicker(t, t.TempDir(), cwd)
	terminal.send(t, "\r")
	result := pickerResult(t, done, "Enter selects the highlighted session")
	if !result.selected || result.path != path {
		t.Fatalf("picked (%q, %v), want (%q, true)", result.path, result.selected, path)
	}
}

// Pi cli/session-picker.ts:37-42 and session-selector.ts:636: the cancel key resolves null, which main.ts:421 reports as "No session selected".
// mutation-checked: reporting a selection after the cancel key fails it
func TestSelectStartupSessionResolvesNullOnCancel(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, t.TempDir())
	cwd := t.TempDir()
	listingPersistedSession(t, defaultSessionDir(cwd), cwd, "only session")
	terminal, _, done := runStartupSessionPicker(t, t.TempDir(), cwd)
	terminal.send(t, "\x1b")
	result := pickerResult(t, done, "Escape cancels")
	if result.selected || result.path != "" {
		t.Fatalf("picked (%q, %v) after cancel, want (\"\", false)", result.path, result.selected)
	}
}

// Pi cli/session-picker.ts:22-23: KeybindingsManager.create() reads the user's keybindings.json and setKeybindings installs it, so a rebound tui.select.cancel is the cancel key.
// mutation-checked: building the picker's keybindings without the agent dir leaves ctrl+x unbound and fails it
func TestSelectStartupSessionUsesTheUsersKeybindings(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, t.TempDir())
	cwd := t.TempDir()
	listingPersistedSession(t, defaultSessionDir(cwd), cwd, "only session")
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"), []byte(`{"tui.select.cancel":"ctrl+x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	terminal, _, done := runStartupSessionPicker(t, agentDir, cwd)
	terminal.send(t, "\x18")
	select {
	case result := <-done:
		if result.err != nil || result.selected {
			t.Fatalf("ctrl+x gave (%q, %v, %v), want a cancellation", result.path, result.selected, result.err)
		}
	case <-time.After(3 * time.Second):
		terminal.send(t, "\x1b")
		t.Fatal("the rebound tui.select.cancel (ctrl+x) did not cancel the picker")
	}
}

// Pi cli/session-picker.ts:42: the picker passes { showRenameHint: false }, so its header offers sort and name filtering but no rename.
// mutation-checked: leaving showRenameHint on in newStartupSessionSelector puts the rename hint in the header and fails it
func TestSelectStartupSessionShowsNoRenameHint(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, t.TempDir())
	cwd := t.TempDir()
	listingPersistedSession(t, defaultSessionDir(cwd), cwd, "only session")
	terminal, out, done := runStartupSessionPicker(t, t.TempDir(), cwd)
	frames := stripANSI(out.String())
	terminal.send(t, "\x1b")
	pickerResult(t, done, "Escape cancels")
	if !strings.Contains(frames, "sort") || !strings.Contains(frames, "named") {
		t.Fatalf("the picker header lost its sort and named hints: %q", frames)
	}
	if strings.Contains(frames, "rename") {
		t.Errorf("the --resume picker shows a rename hint: %q", frames)
	}
}
