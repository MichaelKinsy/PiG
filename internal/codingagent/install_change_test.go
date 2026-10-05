package codingagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/installchange"
)

// PiG's version of Pi 1.0.3's install change warning (#10439), D95. The tests change a real executable on disk: they
// build a small program, record it as the process's install, then replace or remove it.

var (
	installProgramOnce sync.Once
	installProgram     string
	installProgramErr  string
)

func buildInstallProgram(t *testing.T) string {
	t.Helper()
	installProgramOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pig-install-change-")
		if err != nil {
			installProgramErr = err.Error()
			return
		}
		source := filepath.Join(dir, "main.go")
		if err := os.WriteFile(source, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
			installProgramErr = err.Error()
			return
		}
		installProgram = filepath.Join(dir, "program"+installExeSuffix())
		build := exec.Command("go", "build", "-o", installProgram, source)
		build.Dir = dir
		if output, err := build.CombinedOutput(); err != nil {
			installProgramErr = err.Error() + ": " + string(output)
		}
	})
	if installProgramErr != "" {
		t.Fatalf("build program: %s", installProgramErr)
	}
	return installProgram
}

func installExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// installedProgram copies the built program to a directory of its own, so a test can replace or remove it.
func installedProgram(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(buildInstallProgram(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pig"+installExeSuffix())
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// replaceInstall puts a different executable at path while the process that recorded it keeps running.
func replaceInstall(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(buildInstallProgram(t))
	if err != nil {
		t.Fatal(err)
	}
	next := path + ".next"
	if err := os.WriteFile(next, append(data, "updated"...), 0o755); err != nil {
		t.Fatal(err)
	}
	removeInstall(t, path)
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
}

func removeInstall(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows cannot delete a file it runs, but it can move it away.
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func installChangeMode(t *testing.T, tracker *installchange.Tracker) *InteractiveMode {
	t.Helper()
	m := bugHintEventMode(t)
	m.installChanges = tracker
	return m
}

// chatText is the rendered transcript with each wrapped line joined by one space, so a sentence compares whole.
func chatText(m *InteractiveMode) string {
	return strings.Join(strings.Fields(stripANSITest(strings.Join(m.chatContainer.Render(200), "\n"))), " ")
}

// installRemovedWarning is Pi's warning for a removed install (interactive-mode.ts maybeShowInstallChangeWarning, kind "removed") with pig's
// app name, and the restart sentence for output that is not a terminal. A replaced or removed executable shows it.
const installRemovedWarning = "Warning: The pig installation this session runs from was removed or replaced. Features that load code on demand can fail until restart. Restart pig."

// cellsPrunedWarning is the same warning for a pruned extension cell or runtime file (D95).
const cellsPrunedWarning = "Warning: The pig extension files this session runs from were removed or replaced. Features that load code on demand can fail until restart. Restart pig."

// outputNotATerminal makes the restart sentence `Restart pig.`, whatever the test's own stdout is.
func outputNotATerminal(t *testing.T) {
	t.Helper()
	previous := stdoutIsTTY
	t.Cleanup(func() { stdoutIsTTY = previous })
	stdoutIsTTY = func() bool { return false }
}

// installChangeWarned reports whether the transcript shows any install change warning.
func installChangeWarned(m *InteractiveMode) bool {
	return strings.Contains(chatText(m), "this session runs from")
}

// Ports packages/coding-agent/test/interactive-mode-bug-report-hint.test.ts "InteractiveMode bug report hints › shows the install change warning
// instead of a bug report hint" (Pi 1.0.3). The upstream case stubs maybeShowInstallChangeWarning to true. Here the install is a real executable
// that was replaced or removed, and the warning replaces the hint on the assistant error path (interactive-mode.ts maybeSuggestBugReport).
func TestInstallChangeWarningShownInsteadOfABugReportHint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, path string)
	}{
		{"replaced", replaceInstall},
		{"removed", removeInstall},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outputNotATerminal(t)
			path := installedProgram(t)
			m := installChangeMode(t, installchange.NewTracker(path))
			tc.change(t, path)

			endBugHintMessage(m, ai.StopReasonError, "Cannot find module './worker.js'")

			text := chatText(m)
			if !strings.Contains(text, installRemovedWarning) {
				t.Fatalf("no install change warning:\n%s", text)
			}
			if got := bugHintCount(m); got != 0 {
				t.Fatalf("the bug report hint was shown %d times beside the warning", got)
			}
			if m.bugReportHintShown {
				t.Fatal("the warning consumed the one bug report hint")
			}
		})
	}
}

// An untouched install keeps the bug report hint, as Pi's other cases do.
func TestUnchangedInstallStillSuggestsABugReport(t *testing.T) {
	m := installChangeMode(t, installchange.NewTracker(installedProgram(t)))
	endBugHintMessage(m, ai.StopReasonError, "Unexpected internal state")
	if got := bugHintCount(m); got != 1 {
		t.Fatalf("bug report hints = %d, want 1", got)
	}
	if strings.Contains(chatText(m), "restart") {
		t.Fatalf("an untouched install warned:\n%s", chatText(m))
	}
}

// Pi's maybeShowInstallChangeWarning returns true once it has warned, so later errors show no hint and no second warning.
func TestInstallChangeWarningShownOnce(t *testing.T) {
	path := installedProgram(t)
	m := installChangeMode(t, installchange.NewTracker(path))
	removeInstall(t, path)
	endBugHintMessage(m, ai.StopReasonError, "Cannot find module './a.js'")
	endBugHintMessage(m, ai.StopReasonError, "Cannot find module './b.js'")
	m.showError("another failure")
	if got := strings.Count(chatText(m), "this session runs from"); got != 1 {
		t.Fatalf("install change warnings = %d, want 1:\n%s", got, chatText(m))
	}
	if got := bugHintCount(m); got != 0 {
		t.Fatalf("bug report hints = %d, want 0", got)
	}
}

// Pi reports the change when a tool call fails (interactive-mode.ts handleEvent tool_execution_end), also for a tool without a component.
func TestInstallChangeWarningOnAFailedToolCall(t *testing.T) {
	outputNotATerminal(t)
	path := installedProgram(t)
	m := installChangeMode(t, installchange.NewTracker(path))
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "ok", ToolName: "bash", IsError: false})
	if installChangeWarned(m) {
		t.Fatal("a successful tool call warned")
	}
	replaceInstall(t, path)
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "t1", ToolName: "bash", IsError: true})
	if !strings.Contains(chatText(m), installRemovedWarning) {
		t.Fatalf("a failed tool call did not warn:\n%s", chatText(m))
	}
}

// Pi's restart sentence names the session to resume when the session persists (formatResumeCommand), and `Restart pi.` otherwise.
func TestInstallChangeWarningRestartCommand(t *testing.T) {
	previous := stdoutIsTTY
	t.Cleanup(func() { stdoutIsTTY = previous })

	t.Run("persisted session on a terminal", func(t *testing.T) {
		stdoutIsTTY = func() bool { return true }
		path := installedProgram(t)
		m := installChangeMode(t, installchange.NewTracker(path))
		removeInstall(t, path)
		m.showError("failed")
		id := m.currentSession().ID()
		want := "Features that load code on demand can fail until restart. Restart with `pig --session " + id + "` to continue this session."
		if !strings.Contains(chatText(m), want) {
			t.Fatalf("restart sentence missing %q:\n%s", want, chatText(m))
		}
	})
	t.Run("output that is not a terminal", func(t *testing.T) {
		stdoutIsTTY = func() bool { return false }
		path := installedProgram(t)
		m := installChangeMode(t, installchange.NewTracker(path))
		removeInstall(t, path)
		m.showError("failed")
		want := "Features that load code on demand can fail until restart. Restart pig."
		if !strings.Contains(chatText(m), want) {
			t.Fatalf("restart sentence missing %q:\n%s", want, chatText(m))
		}
	})
}

// An extension cell or runtime file that another pig's cache prune removed shows the warning when the failure is shown.
func TestInstallChangeWarningWhenACellFileIsMissing(t *testing.T) {
	for _, show := range []struct {
		name string
		fn   func(m *InteractiveMode)
	}{
		{"error", func(m *InteractiveMode) { m.showError("spawn /cache/cell: no such file or directory") }},
		{"extension error", func(m *InteractiveMode) {
			m.queueExtensionError(&extension.ExtensionError{ExtensionPath: "fixture", Error: "spawn /cache/cell: no such file or directory"})
			m.showPendingExtensionErrors()
		}},
	} {
		t.Run(show.name, func(t *testing.T) {
			outputNotATerminal(t)
			dir := t.TempDir()
			cell := filepath.Join(dir, "cache", "entry", "cell")
			if err := os.MkdirAll(filepath.Dir(cell), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cell, []byte("cell"), 0o755); err != nil {
				t.Fatal(err)
			}
			tracker := installchange.NewTracker(installedProgram(t))
			tracker.TrackFile(cell)
			m := installChangeMode(t, tracker)

			show.fn(m)
			if installChangeWarned(m) {
				t.Fatalf("a present cell warned:\n%s", chatText(m))
			}
			if err := os.RemoveAll(filepath.Dir(cell)); err != nil {
				t.Fatal(err)
			}
			show.fn(m)
			if !strings.Contains(chatText(m), cellsPrunedWarning) {
				t.Fatalf("a pruned cell did not warn:\n%s", chatText(m))
			}
		})
	}
}

// Without Record, as in a test or an embedder that never calls it, nothing is tracked and no error warns.
func TestNoInstallChangeWithoutATracker(t *testing.T) {
	m := bugHintEventMode(t)
	m.showError("failed")
	endBugHintMessage(m, ai.StopReasonError, "Unexpected internal state")
	if installChangeWarned(m) {
		t.Fatalf("an untracked mode warned:\n%s", chatText(m))
	}
}
