package codingagent

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

const scrollbackHeaderRow = "HEADER ROW ABOVE THE TRANSCRIPT, UNCHANGED BY ANY COMMAND"

// scrollbackMode is sessionChromeMode on a terminal that records its bytes: the header row, the transcript of the saved session, the footer and the editor, painted once, as
// the first frame of a TUI that started below text printed before it. The returned buffer is empty when the mode is handed over.
func scrollbackMode(t *testing.T) (*InteractiveMode, *bytes.Buffer) {
	t.Helper()
	m := sessionChromeMode(t)
	return m, recordTerminal(t, m, "answer")
}

// recordTerminal gives m a terminal that records its bytes and paints the header row, m's chat, footer and editor on it once; the first frame must hold wantFirst.
// The returned buffer is empty when it is handed over.
func recordTerminal(t *testing.T, m *InteractiveMode, wantFirst string) *bytes.Buffer {
	t.Helper()
	var terminal bytes.Buffer
	ui := tui.NewWithOutput(&terminal, 80, 30)
	ui.SetRenderDeadline(func(time.Time) {}) // the test owns the frames
	m.tuiInst = ui
	if m.editor == nil {
		m.editor = tui.NewEditor()
	}
	m.editorContainer = tui.NewContainer(m.editor)
	ui.Add(tui.NewText(scrollbackHeaderRow))
	ui.Add(m.chatContainer)
	ui.Add(m.statusLine)
	ui.Add(m.editorContainer)
	m.renderSessionEntries()
	ui.Render()
	if !strings.Contains(terminal.String(), wantFirst) {
		t.Fatalf("the first frame lacks the saved transcript: %q", terminal.String())
	}
	terminal.Reset()
	return &terminal
}

// requireDifferentialFrame fails when the bytes written since the mode was handed over clear the screen or the scrollback, or rewrite the row above the changed rows: Pi
// repaints a replaced transcript from its first changed row (tui-main-screen.ts:456-458 sends a clearing redraw only when that row is above the viewport), so the text the
// terminal holds above the frame, such as what was printed before the TUI started, stays.
func requireDifferentialFrame(t *testing.T, written string, want string) {
	t.Helper()
	for _, sequence := range []string{"\x1b[2J", "\x1b[3J", "\x1b[H"} {
		if strings.Contains(written, sequence) {
			t.Errorf("the frame holds %q, which erases the terminal's scrollback; Pi writes none of it here:\n%q", sequence, written)
		}
	}
	if strings.Contains(written, scrollbackHeaderRow) {
		t.Errorf("the frame rewrites the unchanged header row, so it replays the transcript from the top:\n%q", written)
	}
	if want != "" && !strings.Contains(written, want) {
		t.Errorf("the frame lacks %q, so the command did not repaint:\n%q", want, written)
	}
}

// The session commands and a compaction replace the transcript through the owner's code path (the slash handlers over the runtime, navigateTree, the compaction event),
// and repaint it as Pi's `chatContainer.clear(); renderInitialMessages()` does (interactive-mode.ts:2113-2114 and 5767-5768 for /tree, 2200 and 2311-2319 for /new, /resume,
// /fork and /clone, 3773-3775 for a compaction, 4356-4358 for a rebuild from the session):
// no forced redraw, so the terminal's own scrollback survives.
func TestSessionReplacementRepaintsDifferentially(t *testing.T) {
	firstUserEntry := func(m *InteractiveMode) string {
		for _, entry := range m.currentSession().GetEntries() {
			if entry.Base().Type == "message" {
				return entry.Base().ID
			}
		}
		t.Fatal("the fixture session has no message entry")
		return ""
	}
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, m *InteractiveMode)
		want string
	}{
		{"/new", func(t *testing.T, m *InteractiveMode) {
			if err := newHandler(m.buildSlashContext(context.Background())); err != nil {
				t.Fatal(err)
			}
		}, "New session started"},
		{"/resume", func(t *testing.T, m *InteractiveMode) {
			other, err := NewSessionManagerWithDir(m.opts.CWD, t.TempDir()).Create("other", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range []agentMessage{userMsg("other question"), assistantMsg("", ai.TextContent{Text: "other answer"})} {
				if _, err := other.AppendMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.buildSlashContext(context.Background()).LoadSessionPath(other.Path()); err != nil {
				t.Fatal(err)
			}
		}, "other answer"},
		{"/fork", func(t *testing.T, m *InteractiveMode) {
			sc := m.buildSlashContext(context.Background())
			sc.Args = firstUserEntry(m)
			if err := forkHandler(sc); err != nil {
				t.Fatal(err)
			}
		}, "Forked to new session"},
		{"/clone", func(t *testing.T, m *InteractiveMode) {
			if err := cloneHandler(m.buildSlashContext(context.Background())); err != nil {
				t.Fatal(err)
			}
		}, "Cloned to new session"},
		{"/tree", func(t *testing.T, m *InteractiveMode) {
			if _, err := m.navigateTree(context.Background(), firstUserEntry(m), false, ""); err != nil {
				t.Fatal(err)
			}
		}, ""},
		{"/compact", func(t *testing.T, m *InteractiveMode) {
			m.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", Summary: "SUMMARY", TokensBefore: 200})
		}, "[compaction]"},
		{"a rebuild of the chat (the reload's restore, /settings cache-miss notices)", func(t *testing.T, m *InteractiveMode) {
			m.rebuildChatFromSession()
		}, "answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, terminal := scrollbackMode(t)
			tc.run(t, m)
			m.tuiInst.Render()
			requireDifferentialFrame(t, terminal.String(), tc.want)
		})
	}
}

// Pi's reload box asks for a clearing redraw (`this.ui.requestRender(true)`, interactive-mode.ts:6562), and pig keeps that one forced frame where Pi has it: the frame
// that shows the box starts with the clearing sequence and replays the page. The repaint of the transcript that follows (rebuildChatFromMessages, :6580) is differential.
func TestReloadBoxForcesTheRedrawAsPiDoes(t *testing.T) {
	m, terminal := scrollbackMode(t)
	release := m.beginReloadBlocker()
	if got := terminal.String(); !strings.Contains(got, "\x1b[2J\x1b[H\x1b[3J") || !strings.Contains(got, scrollbackHeaderRow) {
		t.Fatalf("the reload box was drawn without the clearing redraw of Pi's requestRender(true): %q", got)
	}
	terminal.Reset()
	release()
	m.rebuildChatFromSession()
	m.tuiInst.Render()
	requireDifferentialFrame(t, terminal.String(), "answer")
}
