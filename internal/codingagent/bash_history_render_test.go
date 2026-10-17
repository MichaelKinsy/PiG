package codingagent

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// upstream: interactive-mode.ts:3870-3887 addMessageToChat case "bashExecution" renders a persisted `!cmd` entry as a
// BashExecutionComponent (command header, appended output, then exit code / cancelled / truncated from the entry) when a session is
// resumed or the transcript is rebuilt; the entry's outputPad applies (this.outputPad).
func TestResumedBashExecutionEntryRendersLikePi(t *testing.T) {
	m := resumeThinkingMode(t, false, userMsg("before the shell command"))
	cwd := t.TempDir()
	m.opts.CWD = cwd
	m.opts.ResumePath = filepath.Join(cwd, "session.jsonl")
	m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), true)
	inner := m.opts.SessionHandle.(*recordingCompactHandle).inner
	code := 2
	if _, err := inner.AppendBashExecution(BashExecutionMessage{Command: "make test", Output: "one\ntwo\n", ExitCode: &code, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendBashExecution(BashExecutionMessage{Command: "sleep 9", Output: "", Cancelled: true, Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	m.renderInitialMessages()
	got := stripANSITest(strings.Join(m.chatContainer.Render(80), "\n"))
	for _, want := range []string{"before the shell command", "$ make test", "two", "(exit 2)", "$ sleep 9", "(cancelled)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, got)
		}
	}
	if len(m.bashOrder) != 2 {
		t.Fatalf("bash blocks registered for Ctrl+O = %d, want 2", len(m.bashOrder))
	}
	if strings.Index(got, "$ make test") < strings.Index(got, "before the shell command") {
		t.Fatalf("entries rendered out of order:\n%s", got)
	}
}

// upstream: interactive-mode.ts:3870-3887 and :7009/:7037 build a BashExecutionComponent without setExpanded, and the component starts collapsed
// (bash-execution.ts:29 expanded = false), so with tool output expanded a resumed or new `!cmd` block still shows only the last
// PREVIEW_LINES (20) rows until the next toggle (setToolsExpanded, :4562) expands every child.
func TestResumedBashExecutionStartsCollapsedWhileToolsAreExpanded(t *testing.T) {
	m := resumeThinkingMode(t, false, userMsg("before the shell command"))
	cwd := t.TempDir()
	m.opts.CWD = cwd
	m.opts.ResumePath = filepath.Join(cwd, "session.jsonl")
	m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), true)
	inner := m.opts.SessionHandle.(*recordingCompactHandle).inner
	var output strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&output, "row-%02d\n", i)
	}
	code := 0
	if _, err := inner.AppendBashExecution(BashExecutionMessage{Command: "seq 30", Output: output.String(), ExitCode: &code, Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	m.toolsExpanded = true
	m.renderInitialMessages()
	got := stripANSITest(strings.Join(m.chatContainer.Render(80), "\n"))
	if !strings.Contains(got, "row-30") || strings.Contains(got, "row-01") {
		t.Fatalf("a resumed bash block must start collapsed to its last 20 rows:\n%s", got)
	}
}

// interactive-mode.ts:3870-3887 builds the resumed BashExecutionComponent with this.outputPad (constructor argument 4): its header and
// status rows are padded by the setting, not by the default.
func TestResumedBashExecutionEntryUsesOutputPad(t *testing.T) {
	for _, pad := range []int{0, 3} {
		m := resumeThinkingMode(t, false, userMsg("before the shell command"))
		cwd := t.TempDir()
		m.opts.CWD = cwd
		m.opts.ResumePath = filepath.Join(cwd, "session.jsonl")
		m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), true)
		m.outputPad = pad
		inner := m.opts.SessionHandle.(*recordingCompactHandle).inner
		code := 2
		if _, err := inner.AppendBashExecution(BashExecutionMessage{Command: "make test", Output: "one\n", ExitCode: &code, Timestamp: 1}); err != nil {
			t.Fatal(err)
		}
		m.renderInitialMessages()
		found := false
		for _, line := range m.chatContainer.Render(80) {
			if plain := stripANSITest(line); strings.Contains(plain, "$ make test") {
				found = true
				if got := len(plain) - len(strings.TrimLeft(plain, " ")); got != pad {
					t.Fatalf("outputPad %d: header indent = %d (%q)", pad, got, plain)
				}
			}
		}
		if !found {
			t.Fatalf("outputPad %d: no bash header row", pad)
		}
	}
}
