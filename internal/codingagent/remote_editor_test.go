package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type fakeRemoteEditor struct {
	host               extension.RemoteEditorHost
	calls              []string
	configs            []extension.RemoteEditorConfig
	embedWorkingStatus bool
}

func (f *fakeRemoteEditor) EmbedWorkingStatus() bool       { return f.embedWorkingStatus }
func (f *fakeRemoteEditor) Input(data string)              { f.calls = append(f.calls, "input:"+data) }
func (f *fakeRemoteEditor) SetText(text string)            { f.calls = append(f.calls, "setText:"+text) }
func (f *fakeRemoteEditor) InsertTextAtCursor(text string) { f.calls = append(f.calls, "insert:"+text) }
func (f *fakeRemoteEditor) AddToHistory(text string)       { f.calls = append(f.calls, "history:"+text) }
func (f *fakeRemoteEditor) Mouse(extension.RemoteMouseEvent) {
	f.calls = append(f.calls, "mouse")
}
func (f *fakeRemoteEditor) Configure(config extension.RemoteEditorConfig) {
	f.configs = append(f.configs, config)
}
func (f *fakeRemoteEditor) Bind(host extension.RemoteEditorHost) { f.host = host }
func (f *fakeRemoteEditor) Close()                               {}

// runPostedTasks runs the owner-loop tasks the test posted, as the input loop
// would.
func runPostedTasks(t *testing.T, m *InteractiveMode) {
	t.Helper()
	queued := func(q *remoteEventQueue) bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.events) > 0
	}
	for {
		pending := queued(&m.remoteEditorEvents) || queued(&m.frontendActions)
		select {
		case fn := <-m.uiTaskCh:
			fn()
		default:
			if !pending {
				return
			}
			m.remoteEditorEvents.drain()
			m.frontendActions.drain()
		}
	}
}

// Pi's setCustomEditorComponent copies the editor text, settings and focus to
// an installed editor, and every key then goes to the editor's handleInput
// (the extension shortcuts too, which Pi's CustomEditor runs itself). The
// editor's callbacks run the default editor's handlers on the owner loop.
func TestRemoteEditorReceivesKeysAndRunsHostHandlers(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	m.editor.SetText("carried")
	shortcuts := 0
	m.extensionShortcutListener = func(string) bool { shortcuts++; return true }
	fake := &fakeRemoteEditor{}
	m.setRemoteEditor(fake)
	if fake.host == nil || len(fake.configs) != 1 || !fake.configs[0].Focused || !slices.Equal(fake.calls, []string{"setText:carried"}) {
		t.Fatalf("install: host=%v configs=%+v calls=%q", fake.host != nil, fake.configs, fake.calls)
	}

	for _, key := range []string{"i", "\x03", "\x1b", "\x16", "\r"} {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"setText:carried", "input:i", "input:\x03", "input:\x1b", "input:\x16", "input:\r"}; !slices.Equal(fake.calls, want) {
		t.Fatalf("forwarded = %q, want %q", fake.calls, want)
	}
	if shortcuts != 0 {
		t.Fatalf("the host ran %d extension shortcuts itself", shortcuts)
	}

	fake.host.EditorChanged("typed text", "typed text")
	fake.host.EditorFrame([]string{"row"}, 100, false)
	fake.host.EditorShortcut("\x16")
	runPostedTasks(t, m)
	if m.editor.Text() != "typed text" || shortcuts != 1 {
		t.Fatalf("mirror %q, shortcuts %d", m.editor.Text(), shortcuts)
	}
	if got := m.editor.Render(100); !slices.Equal(got, []string{"row"}) {
		t.Fatalf("editor rows = %q", got)
	}

	// app.clear is the default editor's handleCtrlC: clear the editor.
	fake.calls = nil
	fake.host.EditorAction(extension.RemoteEditorAction{Action: "app.clear", Text: "typed text", Expanded: "typed text"})
	runPostedTasks(t, m)
	if !slices.Equal(fake.calls, []string{"setText:"}) {
		t.Fatalf("app.clear sent %q", fake.calls)
	}

	// Escape in bash mode is the default editor's onEscape: leave bash mode.
	fake.calls = nil
	m.isIdle = true
	fake.host.EditorChanged("!ls", "!ls")
	fake.host.EditorAction(extension.RemoteEditorAction{Action: "app.interrupt", Text: "!ls", Expanded: "!ls"})
	runPostedTasks(t, m)
	if !slices.Equal(fake.calls, []string{"setText:"}) {
		t.Fatalf("bash-mode escape sent %q", fake.calls)
	}

	// The thinking level travels with the editor's configuration.
	configs := len(fake.configs)
	m.editor.ThinkingLevel = "high"
	m.editor.Render(100)
	runPostedTasks(t, m)
	if len(fake.configs) != configs+1 || fake.configs[len(fake.configs)-1].ThinkingLevel != "high" {
		t.Fatalf("configs after a thinking change = %+v", fake.configs[configs:])
	}

	// The extension's process ended: the host's editor returns with the text.
	fake.host.EditorChanged("left over", "left over")
	fake.host.EditorClosed()
	runPostedTasks(t, m)
	if m.editor.IsRemote() || m.editor.Text() != "left over" {
		t.Fatalf("after close: remote=%v text=%q", m.editor.IsRemote(), m.editor.Text())
	}
	if err := m.dispatchKey(context.Background(), "\x16"); err != nil {
		t.Fatal(err)
	}
	if shortcuts != 2 {
		t.Fatal("the host's own editor no longer runs extension shortcuts")
	}
}

// Every handler Pi's CustomEditor can call maps to the host's handler for it.
func TestAppActionKeyCoversPiDefaultEditorHandlers(t *testing.T) {
	seen := map[keyAction]AppKeybinding{}
	for _, action := range []AppKeybinding{
		"app.interrupt", "app.exit", "app.clipboard.pasteImage", "app.clear", "app.suspend", "app.thinking.cycle",
		"app.model.cycleForward", "app.model.cycleBackward", "app.model.select", "app.tools.expand", "app.thinking.toggle",
		"app.editor.external", "app.message.copy", "app.message.followUp", "app.message.dequeue", "app.session.new",
		"app.session.tree", "app.session.fork", "app.session.resume",
	} {
		key, ok := appActionKey(action)
		if !ok || key == actionInsert {
			t.Errorf("%s has no handler", action)
		}
		if previous, dup := seen[key]; dup {
			t.Errorf("%s and %s share a handler", previous, action)
		}
		seen[key] = action
	}
	if _, ok := appActionKey("tui.input.submit"); ok {
		t.Error("a tui action mapped to an app handler")
	}
}
