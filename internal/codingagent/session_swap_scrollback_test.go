package codingagent

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
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
// repaints a replaced transcript from its first changed row (tui-main-screen.ts:451 sends a clearing redraw only when that row is above the viewport), so the text the
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
// and repaint it as Pi's `chatContainer.clear(); renderInitialMessages()` does (interactive-mode.ts:2004-2005, 2201-2210, 3445, 3683, 4220-4223, 5634-5635, 6889-6900):
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
			m.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", HasResult: true, Summary: "SUMMARY", TokensBefore: 200})
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

// An extension's boundary compaction entry rebuilds the transcript around its summary: Pi clears the chat container, renders the entries again and requests an ordinary
// render (interactive-mode.ts, the compaction branch of entry_appended), as it does for every other replacement of the transcript.
func TestBoundaryCompactionRepaintsDifferentially(t *testing.T) {
	p, ids := boundaryCompactionMode(t, func(sess *Session, ids map[string]string) {
		appendMessage(t, sess, ids, "old", userMsg("OLD QUESTION"))
		appendMessage(t, sess, ids, "oldAnswer", assistantMsg("", ai.TextContent{Text: "OLD ANSWER"}))
		appendMessage(t, sess, ids, "kept", userMsg("KEPT QUESTION"))
		id, err := sess.AppendCompaction("BOUNDARY SUMMARY", ids["kept"], 1234, nil, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids["compaction"] = id
	})
	terminal := recordTerminal(t, p.InteractiveMode, "KEPT QUESTION")
	p.chatContainer.Add(tui.NewTruncatedText("STALE TRANSCRIPT", 0, 0))
	p.tuiInst.Render()
	terminal.Reset()
	p.handleAgentEvent(appendedEvent(t, p, ids["compaction"]))
	p.tuiInst.Render()
	requireDifferentialFrame(t, terminal.String(), "[compaction]")
}

// /logout of a provider changes no transcript row, and Pi asks for no clearing redraw after it (interactive-mode.ts showOAuthSelector: the logout, then showStatus, which
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

// Pi's reload box asks for a clearing redraw (`this.ui.requestRender(true)`, interactive-mode.ts:6648), and pig keeps that one forced frame where Pi has it: the frame
// that shows the box starts with the clearing sequence and replays the page. The repaint of the transcript that follows is differential.
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

// A frame requested before an awaited session operation is painted before the operation begins (Pi: the key press that closes the picker asks for a frame that runs on the
// next tick, inside the await). The recording terminal shows the order for a swap of 0 ms and of 50 ms: the frame with the old transcript and the selector gone is written
// before the session_before_fork handler runs, and the swaps own frames after it.

// A frame requested before an awaited session operation is painted before the operation begins (Pi: the key press that closes the picker asks for a frame that runs on the
// next tick, inside the await). The recording terminal shows the order for a swap of 0 ms and of 50 ms: the frame with the old transcript and the selector gone is written
// before the session_before_fork handler runs, and the swap's own frames after it.
func TestFrameRequestedBeforeAwaitedSwapIsPaintedFirst(t *testing.T) {
	for _, swap := range []time.Duration{0, 50 * time.Millisecond} {
		t.Run(swap.String(), func(t *testing.T) {
			m, terminal := scrollbackMode(t)
			const selectorRow = "SELECTOR ROW OF THE FORK PICKER"
			// The picker took the editor slot and a frame showed it; closing it restores the editor and requests a frame, which the test's terminal does not throttle-paint.
			m.editorContainer.SetChildren(tui.NewText(selectorRow))
			m.tuiInst.Render()
			m.editorContainer.SetChildren(m.editor)
			m.tuiInst.RequestRender()
			terminal.Reset()

			var atHook string
			handle := m.opts.SessionHandle.(*beforeUIHandle)
			handle.runner = inproc.NewRunner([]extension.Extension{{Path: "slow", Handlers: map[string][]extension.HandlerFn{
				EventSessionBeforeFork: {func(...any) (any, error) {
					atHook = terminal.String()
					time.Sleep(swap)
					return nil, nil
				}},
			}}}, m.opts.CWD)

			sc := m.buildSlashContext(context.Background())
			for _, entry := range m.currentSession().GetEntries() {
				if entry.Base().Type == "message" {
					sc.Args = entry.Base().ID
					break
				}
			}
			if err := forkHandler(sc); err != nil {
				t.Fatal(err)
			}
			m.tuiInst.Render()
			if atHook == "" {
				t.Fatal("no frame was written before the session_before_fork handler ran: the requested frame waited for the swap")
			}
			if strings.Contains(atHook, selectorRow) || strings.Contains(atHook, "Forked to new session") {
				t.Errorf("the frame before the swap is not the old transcript with the selector gone:\n%q", atHook)
			}
			after := strings.TrimPrefix(terminal.String(), atHook)
			if !strings.Contains(after, "Forked to new session") {
				t.Errorf("the swap's frames did not follow the first frame:\n%q", after)
			}
		})
	}
}
