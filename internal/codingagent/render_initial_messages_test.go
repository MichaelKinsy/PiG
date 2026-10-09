package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// upstream: interactive-mode.ts:4168-4182 renderInitialMessages paints the session entries and then the project-trust warning: the warning follows the resumed transcript after a separating spacer, a fresh session has no entries to paint, and a trusted project gets no warning.
func TestRenderInitialMessagesPaintsTheTranscriptThenTheTrustWarning(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	newMode := func(trusted bool, resumePath string) *InteractiveMode {
		m := resumeThinkingMode(t, false, userMsg("resumed prompt"), assistantMsg("", ai.TextContent{Text: "resumed answer"}))
		m.opts.CWD = cwd
		m.opts.ResumePath = resumePath
		m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), trusted)
		return m
	}
	const warning = "This project is not trusted."
	render := func(m *InteractiveMode) string { return stripANSITest(strings.Join(m.chatContainer.Render(200), "\n")) }

	resumed := newMode(false, filepath.Join(cwd, "session.jsonl"))
	resumed.renderInitialMessages()
	got := render(resumed)
	prompt, answer, notice := strings.Index(got, "resumed prompt"), strings.Index(got, "resumed answer"), strings.Index(got, warning)
	if prompt < 0 || answer < prompt || notice < answer {
		t.Errorf("a resumed untrusted project renders the prompt, the answer and then the warning, got %q", got)
	}

	fresh := newMode(false, "")
	fresh.renderInitialMessages()
	if got := render(fresh); strings.Contains(got, "resumed") || !strings.Contains(got, warning) {
		t.Errorf("a fresh untrusted project paints no entries but the warning, got %q", got)
	}

	trusted := newMode(true, filepath.Join(cwd, "session.jsonl"))
	trusted.renderInitialMessages()
	if got := render(trusted); !strings.Contains(got, "resumed answer") || strings.Contains(got, warning) {
		t.Errorf("a trusted project paints the entries without the warning, got %q", got)
	}
}

// upstream: interactive-mode.ts:2201-2210 renderCurrentSessionState (a Session replacement) and :2004-2005, :5634-5635 (tree
// navigation) clear the transcript and call renderInitialMessages, so an untrusted project shows its warning again below the
// repainted entries. Rebuilding the transcript for other reasons (rebuildChatFromMessages, :4220-4223) paints the entries only.
func TestRepaintAfterReplacementOrNavigationShowsTheTrustWarning(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	const warning = "This project is not trusted."
	newMode := func(trusted bool) *InteractiveMode {
		m := resumeThinkingMode(t, false, userMsg("kept prompt"), assistantMsg("", ai.TextContent{Text: "kept answer"}))
		m.opts.CWD = cwd
		m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), trusted)
		m.chatContainer.Add(tui.NewText("stale line"))
		return m
	}
	render := func(m *InteractiveMode) string { return stripANSITest(strings.Join(m.chatContainer.Render(200), "\n")) }
	for name, repaint := range map[string]func(*InteractiveMode){
		"renderCurrentSessionState": (*InteractiveMode).renderCurrentSessionState,
		"repaintInitialMessages":    (*InteractiveMode).repaintInitialMessages,
	} {
		untrusted := newMode(false)
		repaint(untrusted)
		got := render(untrusted)
		answer, notice := strings.Index(got, "kept answer"), strings.Index(got, warning)
		if strings.Contains(got, "stale line") || answer < 0 || notice < answer || strings.Count(got, warning) != 1 {
			t.Errorf("%s: an untrusted project repaints the entries and then one warning, got %q", name, got)
		}
		trusted := newMode(true)
		repaint(trusted)
		if got := render(trusted); !strings.Contains(got, "kept answer") || strings.Contains(got, warning) {
			t.Errorf("%s: a trusted project repaints the entries without the warning, got %q", name, got)
		}
	}
	plain := newMode(false)
	plain.rebuildChatFromSession()
	if got := render(plain); !strings.Contains(got, "kept answer") || strings.Contains(got, warning) {
		t.Errorf("rebuildChatFromSession (rebuildChatFromMessages) paints the entries only, got %q", got)
	}
}
