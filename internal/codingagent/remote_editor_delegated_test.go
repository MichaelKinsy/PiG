package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type fakeDelegatedEditor struct{ fakeRemoteEditor }

func (*fakeDelegatedEditor) Delegated() bool { return true }

func baseOp(t *testing.T, h extension.RemoteEditorHost, op string, args any) map[string]any {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := h.(*remoteEditor).editorBase(op, data)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// A delegated editor component is Pi's CustomEditor subclass: the host's keys and text operations reach the component, and the component's `super` calls reach the host's stock editor, its app-action dispatch and its extension shortcuts. The component's frame, not the stock rows, is what the screen shows.
func TestDelegatedEditorBaseIsTheHostsStockEditor(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	m.editor.SetText("carried")
	var shortcutKeys []string
	m.extensionShortcutListener = func(data string) bool { shortcutKeys = append(shortcutKeys, data); return data == "\x16" }
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)
	h := fake.host

	if !m.editor.IsDelegated() {
		t.Fatal("a delegated editor installed as an ordinary remote")
	}
	if err := m.dispatchKey(t.Context(), "x"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fake.calls, "input:x") || m.editor.Text() != "carried" {
		t.Fatalf("host key: calls=%q text=%q; want the component to see it and the text untouched", fake.calls, m.editor.Text())
	}

	// super.handleInput: editing.
	baseOp(t, h, "handleInput", map[string]string{"data": "x"})
	if got := baseOp(t, h, "getText", nil)["text"]; got != "carriedx" {
		t.Fatalf("getText after super.handleInput = %v", got)
	}
	if m.editor.Text() != "carriedx" {
		t.Fatalf("host editor text = %q", m.editor.Text())
	}
	cursor := baseOp(t, h, "getCursor", nil)
	if cursor["line"] != float64(0) || cursor["col"] != float64(8) {
		t.Fatalf("getCursor = %v", cursor)
	}

	// super.render is the stock rows while the screen shows the component's frame.
	h.EditorFrame([]string{"component row"}, 40, false)
	runPostedTasks(t, m)
	rows, _ := baseOp(t, h, "render", map[string]int{"width": 40})["lines"].([]any)
	if len(rows) == 0 || !strings.Contains(rows[len(rows)-2].(string)+rows[1].(string), "carriedx") {
		t.Fatalf("super.render rows = %v", rows)
	}
	if got := m.editor.Render(40); !slices.Equal(got, []string{"component row"}) {
		t.Fatalf("screen rows = %q", got)
	}

	// The host's own text operations go to the component, which calls super.
	fake.calls = nil
	m.editor.SetText("replaced")
	m.editor.AddToHistory("older")
	m.editor.InsertTextAtCursor("!")
	if want := []string{"setText:replaced", "history:older", "insert:!"}; !slices.Equal(fake.calls, want) {
		t.Fatalf("host operations reached the component as %q; want %q", fake.calls, want)
	}
	baseOp(t, h, "setText", map[string]string{"text": "replaced"})
	baseOp(t, h, "insertTextAtCursor", map[string]string{"text": "!"})
	if got := m.editor.GetExpandedText(); got != "replaced!" {
		t.Fatalf("expanded text = %q", got)
	}

	// super.handleInput runs the default editor's extension shortcut first, then the app action.
	baseOp(t, h, "handleInput", map[string]string{"data": "\x16"})
	if !slices.Equal(shortcutKeys, []string{"x", "\x16"}) || m.editor.Text() != "replaced!" {
		t.Fatalf("extension shortcuts consulted for %q, text %q; want every key consulted, the bound one consumed", shortcutKeys, m.editor.Text())
	}
	m.isIdle = true
	baseOp(t, h, "handleInput", map[string]string{"data": "\x03"})
	if m.editor.Text() != "" {
		t.Fatalf("app.clear through super.handleInput left %q", m.editor.Text())
	}
}

// Each key the host sends a component is acknowledged once (ui.editor.inputDone). A super.handleInput that runs an app
// action releases its key's pump ticket early, so an action that opens a modal input loop can read the next key. The
// late acknowledgement of that key must not release the next key's ticket: the pump would route a third key while the
// component still handles the second, whose action (for example a selector) Pi has already run when the third arrives.
func TestDelegatedEarlyReleaseKeepsAcknowledgementsPaired(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)
	h := m.remoteEditor

	first, second := newInputTicket(), newInputTicket()
	h.ticket = first
	h.Input("\x03")
	// The component calls super.handleInput for an app action while it handles the first key.
	m.isIdle = true
	baseOp(t, fake.host, "handleInput", map[string]string{"data": "\x03"})
	if !first.settled() {
		t.Fatal("super.handleInput of an app action did not release the key's ticket")
	}
	h.ticket = second
	h.Input("x")
	// The first key's acknowledgement arrives after the pump routed the second key.
	fake.host.EditorInputDone()
	runPostedTasks(t, m)
	if second.settled() {
		t.Fatal("the first key's late acknowledgement released the second key's ticket")
	}
	fake.host.EditorInputDone()
	runPostedTasks(t, m)
	if !second.settled() {
		t.Fatal("the second key's acknowledgement did not release its ticket")
	}
}

type embeddingDelegatedEditor struct {
	fakeDelegatedEditor
	embed bool
}

func (f *embeddingDelegatedEditor) EmbedWorkingStatus() bool { return f.embed }

// Pi's CustomEditor subclass with embedWorkingStatus draws the working status in its own border, through the default editor's render. A delegated component's `super.render` is the host's stock editor, so the host hands it the indicator when the component opts in, whenever it opts in.
func TestDelegatedEditorEmbedsTheWorkingStatusWhenItOptsIn(t *testing.T) {
	m := statusBorderMode(t, true)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	custom := &embeddingDelegatedEditor{}
	m.setRemoteEditor(custom)
	t.Cleanup(func() { m.clearStatusIndicator("") })
	hasStatus := func() bool {
		rows, _ := baseOp(t, custom.host, "render", map[string]int{"width": 60})["lines"].([]any)
		return len(rows) > 0 && strings.Contains(rows[0].(string), "Working")
	}

	m.handleAgentEvent(agent.AgentStartEvent{})
	if m.activeWorkingIndicatorEmbedded || m.statusContainer.IsEmpty() || hasStatus() {
		t.Fatalf("an editor that did not opt in: embedded=%v container empty=%v status in border=%v", m.activeWorkingIndicatorEmbedded, m.statusContainer.IsEmpty(), hasStatus())
	}

	// The component opts in after its factory returned: the host moves the status into the border.
	custom.embed = true
	custom.host.(interface{ EditorEmbedWorkingStatusChanged() }).EditorEmbedWorkingStatusChanged()
	runPostedTasks(t, m)
	if !m.activeWorkingIndicatorEmbedded || !m.statusContainer.IsEmpty() || !hasStatus() {
		t.Fatalf("after the opt-in: embedded=%v container empty=%v status in border=%v", m.activeWorkingIndicatorEmbedded, m.statusContainer.IsEmpty(), hasStatus())
	}

	// The next status goes straight to the border, and the indicator leaves it when the work ends.
	m.clearStatusIndicator("")
	m.handleAgentEvent(agent.AgentStartEvent{})
	if !m.activeWorkingIndicatorEmbedded || !hasStatus() {
		t.Fatalf("a new status: embedded=%v status in border=%v", m.activeWorkingIndicatorEmbedded, hasStatus())
	}
	m.clearStatusIndicator("")
	if hasStatus() {
		t.Fatal("the indicator stayed in the border after the work ended")
	}
}

// The editor holds JavaScript strings: a lone UTF-16 unit in its text survives getText, getLines and render as a standard surrogate escape on the wire, not as U+FFFD.
func TestDelegatedEditorBaseRepliesKeepLoneSurrogates(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)
	m.editor.AsBase(func() { m.editor.SetText("A\xed\xa0\xbdB") })
	for op, args := range map[string]string{"getText": ``, "getExpandedText": ``, "getLines": ``, "render": `{"width":40}`} {
		raw, err := fake.host.(*remoteEditor).editorBase(op, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if !strings.Contains(string(raw), `A\ud83dB`) {
			t.Errorf("%s reply = %s; want the lone surrogate escaped as \\ud83d", op, raw)
		}
	}
}

// Pi's custom editor is a new Editor: Up in it recalls nothing submitted before the install, and the default editor it
// restores still recalls its own prompts (interactive-mode.ts setCustomEditorComponent, this.editor.addToHistory).
func TestDelegatedEditorHistoryIsTheComponentsOwn(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.AddToHistory("before")
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)
	baseOp(t, fake.host, "handleInput", map[string]string{"data": "\x1b[A"})
	if got := m.editor.Text(); got != "" {
		t.Fatalf("Up in the new component's base recalled %q; Pi's new Editor has no history", got)
	}
	m.setRemoteEditor(nil)
	m.editor.HandleInput("\x1b[A")
	if got := m.editor.Text(); got != "before" {
		t.Fatalf("Up in the restored default editor = %q; want its own prompt", got)
	}
}
