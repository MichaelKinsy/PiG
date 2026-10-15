package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func compactedSession(t *testing.T, compactions int) func(sess *Session) {
	return func(sess *Session) {
		t.Helper()
		first, err := sess.AppendMessage(userMsg("first question"))
		if err != nil {
			t.Fatal(err)
		}
		for range compactions {
			if _, err := sess.AppendCompaction("summary", first, 100, nil, false, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "answer"})); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Pi shows "Session compacted N time(s)" only at the end of renderInitialMessages (interactive-mode.ts:4178-4182): after a Session is loaded, replaced or
// navigated to, counting every compaction entry of the file. Found by the compaction journey: PiG also flashed it whenever the transcript was rebuilt after a
// compaction, where Pi's rebuildChatFromMessages (interactive-mode.ts:3346) shows nothing.
func TestSessionCompactedStatusIsShownByInitialRenderOnly(t *testing.T) {
	for _, tc := range []struct {
		compactions int
		want        string
	}{{1, "Session compacted 1 time"}, {2, "Session compacted 2 times"}} {
		m := resumedModeWithOutputPad(t, 1, compactedSession(t, tc.compactions))
		m.repaintInitialMessages()
		if got := renderedChat(m); !strings.Contains(got, tc.want) {
			t.Errorf("%d compactions: initial render lacks %q:\n%s", tc.compactions, tc.want, got)
		}
	}

	// The startup path (--resume, --continue) paints through renderInitialMessages, not the repaint after a replacement.
	startup := resumedModeWithOutputPad(t, 1, compactedSession(t, 2))
	startup.opts.ResumePath = startup.currentSession().Path()
	startup.renderInitialMessages()
	if got := renderedChat(startup); !strings.Contains(got, "Session compacted 2 times") {
		t.Errorf("startup render lacks the compaction notice:\n%s", got)
	}

	none := resumedModeWithOutputPad(t, 1, compactedSession(t, 0))
	none.repaintInitialMessages()
	if got := renderedChat(none); strings.Contains(got, "Session compacted") {
		t.Errorf("a Session without compactions shows the notice:\n%s", got)
	}

	rebuilt := resumedModeWithOutputPad(t, 1, compactedSession(t, 2))
	rebuilt.rebuildChatFromSession()
	if got := renderedChat(rebuilt); strings.Contains(got, "Session compacted") {
		t.Errorf("rebuilding the transcript shows the initial-render notice:\n%s", got)
	}

	afterCompact := resumedModeWithOutputPad(t, 1, compactedSession(t, 1))
	entries := afterCompact.currentSession().GetBranch()
	afterCompact.renderCompactionResult(agent.CompactionEndEvent{Summary: "again", TokensBefore: 50}, entries)
	if got := renderedChat(afterCompact); strings.Contains(got, "Session compacted") {
		t.Errorf("the transcript after a manual compaction shows the notice:\n%s", got)
	}
}

// interactive-mode.ts:4208-4223 renderInitialMessages paints the entries, then the project-trust warning, then the compaction notice: with both an untrusted
// project and a compacted Session the notice is the last thing painted.
func TestRenderInitialMessagesShowsTheTrustWarningBeforeTheCompactionNotice(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := resumedModeWithOutputPad(t, 1, compactedSession(t, 2))
	m.opts.CWD = cwd
	m.opts.SettingsManager = NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), false)
	m.opts.ResumePath = m.currentSession().Path()
	m.renderInitialMessages()
	got := renderedChat(m)
	warning, notice := strings.Index(got, "This project is not trusted."), strings.Index(got, "Session compacted 2 times")
	if warning < 0 || notice < 0 || notice < warning {
		t.Errorf("want the trust warning (at %d) before the compaction notice (at %d):\n%s", warning, notice, got)
	}
}
