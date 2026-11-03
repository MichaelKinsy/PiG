package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// D228 tests: the queued-message row and the copy tip name the configured dequeue key (Pi interactive-mode.ts:4696
// getAppKeyDisplay), so a rebinding shows correctly.

func queuedHintRow(m *InteractiveMode) string {
	m.compactionQueue = []compactionQueuedMessage{{text: "x", mode: compactionQueueSteer}}
	m.updatePendingMessagesDisplay()
	return strings.TrimSpace(widthx.StripAnsi(renderPending(m)))
}

func TestQueuedHintAndTipNameTheConfiguredDequeueKey(t *testing.T) {
	defaultKey := altKeyText() + "+Up"
	for _, tc := range []struct {
		name        string
		termProgram string
		keys        []KeyID
		editor      string
		wantHint    string
		wantTipKey  string
		wantMapping bool
	}{
		{"Terminal.app default, empty editor", "Apple_Terminal", nil, "", "↳ Up to edit all queued messages", defaultKey, false},
		{"Terminal.app default, typed text", "Apple_Terminal", nil, "draft", "↳ " + defaultKey + " to edit all queued messages", defaultKey, false},
		{"Terminal.app, alt+up and ctrl+q", "Apple_Terminal", []KeyID{"alt+up", "ctrl+q"}, "", "↳ Up/Ctrl+Q to edit all queued messages", defaultKey + "/Ctrl+Q", false},
		{"Terminal.app, moved to ctrl+q", "Apple_Terminal", []KeyID{"ctrl+q"}, "", "↳ Ctrl+Q to edit all queued messages", "Ctrl+Q", false},
		{"tmux default", "tmux", nil, "", "↳ " + defaultKey + " to edit all queued messages", defaultKey, true},
		{"tmux, moved to ctrl+q", "tmux", []KeyID{"ctrl+q"}, "", "↳ Ctrl+Q to edit all queued messages", "Ctrl+Q", false},
		{"unset default", "", nil, "", "↳ " + defaultKey + " to edit all queued messages", defaultKey, true},
		{"ghostty default", "ghostty", nil, "", "↳ " + defaultKey + " to edit all queued messages", defaultKey, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", tc.termProgram)
			t.Setenv("TERM", "xterm-256color")
			m := newPendingDisplayHarness(t)
			if tc.keys != nil {
				useKeybindings(t, map[string][]KeyID{"app.message.dequeue": tc.keys})
			}
			m.editor.SetText(tc.editor)
			if got := queuedHintRow(m); !strings.HasSuffix(got, tc.wantHint) {
				t.Fatalf("queued hint row = %q, want it to end with %q", got, tc.wantHint)
			}

			// The tip: Up recalls the history entry while the message is queued.
			m.widgetContainer, m.widgetContainerBelow = tui.NewContainer(), tui.NewContainer()
			m.editor.SetText("")
			m.editor.AddToHistory("older prompt")
			recalled := m.watchQueuedHistoryRecall("\x1b[A")
			if recalled == nil {
				t.Fatal("no recall watch for Up with a queued message")
			}
			m.editor.HandleInput("\x1b[A")
			recalled()
			below := strings.Join(strings.Fields(widthx.StripAnsi(strings.Join(m.widgetContainerBelow.Render(200), "\n"))), " ")
			wantTip := "↳ That's a copy. The queued message still sends. " + tc.wantTipKey + " edits the queue."
			if !strings.Contains(below, wantTip) {
				t.Fatalf("tip = %q, want it to contain %q", below, wantTip)
			}
			if got := strings.Contains(below, `map Option+Up to \033[1;3A in Terminal Settings > Profiles > Keyboard.`); got != tc.wantMapping {
				t.Fatalf("mapping hint in %q = %v, want %v", below, got, tc.wantMapping)
			}
		})
	}
}

// The tip is shown once per process run: a second recall, after the tip went with the emptied queue, shows nothing.
func TestQueuedHistoryRecallTipShowsOnceAndGoesWithTheQueue(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	m := newPendingDisplayHarness(t)
	m.widgetContainer, m.widgetContainerBelow = tui.NewContainer(), tui.NewContainer()
	m.editor.AddToHistory("older prompt")
	queuedHintRow(m)
	recall := func() {
		m.editor.SetText("")
		if after := m.watchQueuedHistoryRecall("\x1b[A"); after != nil {
			m.editor.HandleInput("\x1b[A")
			after()
		} else {
			m.editor.HandleInput("\x1b[A")
		}
	}
	recall()
	if rows := m.widgetContainerBelow.Render(200); len(rows) == 0 {
		t.Fatal("no tip after the first recall")
	}
	m.compactionQueue = nil
	m.updatePendingMessagesDisplay()
	if rows := m.widgetContainerBelow.Render(200); len(rows) != 0 {
		t.Fatalf("tip stays after the queue emptied: %q", rows)
	}
	queuedHintRow(m)
	recall()
	if rows := m.widgetContainerBelow.Render(200); len(rows) != 0 {
		t.Fatalf("second recall in the same run showed a tip: %q", rows)
	}
}

// inertEditorRemote is an extension editor component that takes every call and does nothing.
type inertEditorRemote struct{}

func (inertEditorRemote) Input(string)              {}
func (inertEditorRemote) SetText(string)            {}
func (inertEditorRemote) InsertTextAtCursor(string) {}
func (inertEditorRemote) AddToHistory(string)       {}
func (inertEditorRemote) Mouse(tui.TuiMouseEvent)   {}
func (inertEditorRemote) StateChanged()             {}

// D228 changes the built-in editor only. An extension's custom editor, in an extension process (a delegated remote editor, whose keys reach the
// host through AsBase) or in the host (an installed editor factory), keeps Pi's behavior exactly: plain Up neither dequeues in Terminal.app nor
// shows the copy tip.
func TestExtensionEditorKeepsPiUpBehavior(t *testing.T) {
	for _, program := range []string{"Apple_Terminal", "tmux"} {
		for _, kind := range []string{"delegated remote editor", "installed editor factory"} {
			t.Run(program+"/"+kind, func(t *testing.T) {
				t.Setenv("TERM_PROGRAM", program)
				m := newPendingDisplayHarness(t)
				m.widgetContainer, m.widgetContainerBelow = tui.NewContainer(), tui.NewContainer()
				switch kind {
				case "delegated remote editor":
					m.editor.SetDelegatedRemote(inertEditorRemote{})
				default:
					m.editorFactory = func(tui.TUI, tui.EditorTheme, extension.KeybindingsManager) tui.EditorComponent { return nil }
				}
				m.compactionQueue = []compactionQueuedMessage{{text: "queued", mode: compactionQueueSteer}}
				m.updatePendingMessagesDisplay()
				var err error
				m.editor.AsBase(func() {
					m.editor.AddToHistory("older prompt")
					err = m.handleEditorAction(t.Context(), actionInsert, "\x1b[A")
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(m.compactionQueue) != 1 {
					t.Fatalf("queue after Up = %v, want the message still queued (Pi's Up is not dequeue in an extension editor)", m.compactionQueue)
				}
				if rows := m.widgetContainerBelow.Render(200); len(rows) != 0 {
					t.Fatalf("copy tip under an extension editor: %q", rows)
				}
				if hint := queuedHintRow(m); strings.Contains(hint, "↳ Up ") {
					t.Fatalf("queued hint names Up under an extension editor: %q", hint)
				}
			})
		}
	}
}
