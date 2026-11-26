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
	lines := m.chatContainer.Render(80)
			for i, l := range lines {
				lines[i] = strings.TrimRight(stripANSITest(l), " ")
			}
			got := strings.Join(lines, "\n")
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

// Port of Pi main 0ab23c37b074252d04e31f5cfb99491f0613fb80 (packages/coding-agent/test/interactive-mode-status.test.ts):
// rebuilt `!` output follows ctrl+o (toolsExpanded) state.
func TestResumedBashExecutionFollowsToolsExpanded(t *testing.T) {
	for _, expanded := range []bool{true, false} {
		t.Run(fmt.Sprintf("expanded=%v", expanded), func(t *testing.T) {
			m := resumeThinkingMode(t, false, userMsg("before the shell command"))
			cwd := t.TempDir()
			m.opts.CWD = cwd
			m.opts.ResumePath = filepath.Join(cwd, "session.jsonl")
			m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), true)
			inner := m.opts.SessionHandle.(*recordingCompactHandle).inner
			var output strings.Builder
			for i := 1; i <= 40; i++ {
				fmt.Fprintf(&output, "line-%d\n", i)
			}
			code := 0
			if _, err := inner.AppendBashExecution(BashExecutionMessage{Command: "seq 1 40", Output: output.String(), ExitCode: &code, Timestamp: 1}); err != nil {
				t.Fatal(err)
			}
			m.toolsExpanded = expanded
			m.renderInitialMessages()
			lines := m.chatContainer.Render(80)
			for i, l := range lines {
				lines[i] = strings.TrimRight(stripANSITest(l), " ")
			}
			got := strings.Join(lines, "\n")
			if !strings.Contains(got, "line-40") {
				t.Fatalf("missing line-40:\n%s", got)
			}
			if strings.Contains(got, "line-1\n") != expanded {
				t.Fatalf("line-1 visibility = %v, want %v:\n%s", strings.Contains(got, "line-1\n"), expanded, got)
			}
			if strings.Contains(got, "to expand") != !expanded {
				t.Fatalf("expand hint visibility = %v, want %v:\n%s", strings.Contains(got, "to expand"), !expanded, got)
			}
		})
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
