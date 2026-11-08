package codingagent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// In-process ctx.ui.custom (.upstream/v0.99.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2863-2935
// showExtensionCustom): the factory runs with the tui, theme, keybindings and `done`; the component replaces the editor
// slot (or shows as an overlay) with focus; `done(result)` restores the editor with its text, resolves the call with
// the result once, and only then disposes the component. Upstream has no test of it in this scope; the cases follow
// the branches of the function.

type customProbe struct {
	mu       sync.Mutex
	inputs   []string
	disposed atomic.Int32
	label    string
}

func (c *customProbe) Render(int) []string { return []string{c.label} }
func (c *customProbe) Invalidate()         {}
func (c *customProbe) HandleInput(data string) {
	c.mu.Lock()
	c.inputs = append(c.inputs, data)
	c.mu.Unlock()
}
func (c *customProbe) Dispose() { c.disposed.Add(1) }
func (c *customProbe) received() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.inputs...)
}

type customOutcome struct {
	value any
	err   error
}

type customRig struct {
	m       *InteractiveMode
	u       *ExtUIContext
	cancel  context.CancelFunc
	workers sync.WaitGroup
}

func newCustomRig(t *testing.T) *customRig {
	t.Helper()
	m := newSwitchTuiProbe(t)
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx = ctx
	r := &customRig{m: m, u: &ExtUIContext{m: m}, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		r.workers.Wait()
		m.teardownCurrentTui()
		m.tuiInst.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	})
	return r
}

// start runs Custom in a goroutine and pumps the owner loop until the call ends or ready reports true.
func (r *customRig) start(ctx context.Context, factory extension.CustomFactory, opts *extension.CustomOptions) <-chan customOutcome {
	out := make(chan customOutcome, 1)
	r.workers.Go(func() {
		value, err := r.u.Custom(ctx, factory, opts)
		out <- customOutcome{value, err}
	})
	return out
}

// pump runs the owner loop's tasks until done reports true or the call ends.
func (r *customRig) pump(t *testing.T, out <-chan customOutcome, done func() bool) (customOutcome, bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !done() {
		select {
		case fn := <-r.m.uiTaskCh:
			fn()
		case got := <-out:
			return got, true
		case <-timer.C:
			t.Fatal("the owner loop never reached the expected state")
		case <-time.After(time.Millisecond):
		}
	}
	return customOutcome{}, false
}

func (r *customRig) finish(t *testing.T, out <-chan customOutcome) customOutcome {
	t.Helper()
	got, ended := r.pump(t, out, func() bool { return false })
	if !ended {
		t.Fatal("unreachable")
	}
	return got
}

func TestExtUIContextCustomMountsTheComponentInTheEditorSlotAndRestoresItOnDone(t *testing.T) {
	r := newCustomRig(t)
	r.m.editor.SetText("draft text")
	component := &customProbe{label: "CUSTOM"}
	var done func(any)
	var gotHost extension.TUI
	var gotTheme any
	var gotKeybindings extension.KeybindingsManager
	out := r.start(t.Context(), extension.CustomFactory(func(host extension.TUI, theme *tui.Theme, keybindings extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		gotHost, gotTheme, gotKeybindings, done = host, theme, keybindings, d
		return component, nil
	}), nil)
	if _, ended := r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component }); ended {
		t.Fatal("Custom returned before done")
	}
	if gotHost != r.m.tuiInst {
		t.Fatalf("the factory got %v, want the mounted TUI (interactive-mode.ts:3008 passes this.ui)", gotHost)
	}
	// interactive-mode.ts:3008 factory(this.ui, theme, this.keybindings, close): the keybindings are the session's manager, never nil.
	if gotHost == nil || gotTheme != any(tui.ActiveTheme()) || gotKeybindings == nil || gotKeybindings != tui.GetTUIKeybindings() {
		t.Fatalf("factory arguments: host %v theme %v keybindings %v", gotHost, gotTheme, gotKeybindings)
	}
	gotHost.RequestRender() // safe from any goroutine
	// Input reaches the component, not the editor.
	if err := r.m.dispatchKey(t.Context(), "x"); err != nil {
		t.Fatal(err)
	}
	if got := component.received(); len(got) != 1 || got[0] != "x" {
		t.Fatalf("component input = %q", got)
	}
	if r.m.editor.Text() != "draft text" {
		t.Fatalf("the editor took the input: %q", r.m.editor.Text())
	}
	// done from another goroutine resolves the call; a second done is ignored.
	go func() {
		done("first")
		done("second")
	}()
	got := r.finish(t, out)
	if got.err != nil || got.value != "first" {
		t.Fatalf("Custom = %v, %v", got.value, got.err)
	}
	if component.disposed.Load() != 1 {
		t.Fatalf("disposed %d times", component.disposed.Load())
	}
	if any(r.m.tuiInst.GetFocusedComponent()) != any(r.m.editor) {
		t.Fatal("focus did not return to the editor")
	}
	if r.m.editor.Text() != "draft text" {
		t.Fatalf("editor text = %q", r.m.editor.Text())
	}
}

// interactive-mode.ts:2880-2888: restoreEditor puts back the text the editor held when the component opened.
func TestExtUIContextCustomRestoresTheEditorTextItHeldAtOpen(t *testing.T) {
	r := newCustomRig(t)
	r.m.editor.SetText("before")
	var done func(any)
	out := r.start(t.Context(), extension.CustomFactory(func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return &customProbe{label: "C"}, nil
	}), nil)
	r.pump(t, out, func() bool { return done != nil && r.m.editorContainer.Children()[0] != tui.Component(r.m.editor) })
	r.m.editor.SetText("changed while open")
	done(nil)
	r.finish(t, out)
	if r.m.editor.Text() != "before" {
		t.Fatalf("editor text = %q, want the text at open", r.m.editor.Text())
	}
}

// interactive-mode.ts:2900-2903: done called before the factory returns mounts nothing.
func TestExtUIContextCustomDoneBeforeTheFactoryReturnsMountsNothing(t *testing.T) {
	r := newCustomRig(t)
	component := &customProbe{label: "NEVER"}
	out := r.start(t.Context(), extension.CustomFactory(func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		d("early")
		return component, nil
	}), nil)
	got := r.finish(t, out)
	if got.err != nil || got.value != "early" {
		t.Fatalf("Custom = %v, %v", got.value, got.err)
	}
	if any(r.m.tuiInst.GetFocusedComponent()) == component || r.m.editorContainer.Children()[0] != tui.Component(r.m.editor) {
		t.Fatal("a closed component was mounted")
	}
}

// interactive-mode.ts:2887-2893: done called inside the factory still runs close(), whose restoreEditor puts back the
// editor text the call saved at open.
func TestExtUIContextCustomDoneBeforeTheFactoryReturnsRestoresTheEditor(t *testing.T) {
	r := newCustomRig(t)
	r.m.editor.SetText("before")
	out := r.start(t.Context(), extension.CustomFactory(func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		r.m.editor.SetText("changed by the factory")
		d("early")
		return &customProbe{label: "NEVER"}, nil
	}), nil)
	if got := r.finish(t, out); got.err != nil || got.value != "early" {
		t.Fatalf("Custom = %v, %v", got.value, got.err)
	}
	if r.m.editor.Text() != "before" {
		t.Fatalf("editor text = %q, want the text at open", r.m.editor.Text())
	}
}

// interactive-mode.ts showExtensionCustom: a factory error runs restoreEditor, which with no component mounted only puts back the editor text
// (Pi bdd9f039: no clear/addChild/setFocus), and rejects the call. The editor slot and the focus stay as they were.
func TestExtUIContextCustomFactoryErrorRejects(t *testing.T) {
	r := newCustomRig(t)
	boom := errors.New("boom")
	out := r.start(t.Context(), extension.CustomFactory(func(extension.TUI, *tui.Theme, extension.KeybindingsManager, func(any)) (extension.DisposableComponent, error) {
		return nil, boom
	}), nil)
	got := r.finish(t, out)
	if !errors.Is(got.err, boom) {
		t.Fatalf("err = %v", got.err)
	}
	if children := r.m.editorContainer.Children(); len(children) != 1 || children[0] != tui.Component(r.m.editor) || openExtensionDialogs(r.m) != 0 {
		t.Fatalf("editor slot after the factory error = %v with %d dialogs, want the editor alone", children, openExtensionDialogs(r.m))
	}
}

// interactive-mode.ts:2905-2924: an overlay is shown over the screen, leaves the editor in place, and hides on done.
func TestExtUIContextCustomOverlay(t *testing.T) {
	r := newCustomRig(t)
	component := &customProbe{label: "OVERLAY"}
	var done func(any)
	out := r.start(t.Context(), extension.CustomFactory(func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return component, nil
	}), &extension.CustomOptions{Overlay: true})
	r.pump(t, out, func() bool { return r.m.tuiInst.ActiveOverlay() != nil })
	if r.m.editorContainer.Children()[0] != tui.Component(r.m.editor) {
		t.Fatal("an overlay replaced the editor slot")
	}
	if err := r.m.dispatchKey(t.Context(), "y"); err != nil {
		t.Fatal(err)
	}
	if got := component.received(); len(got) != 1 || got[0] != "y" {
		t.Fatalf("overlay input = %q", got)
	}
	done("closed")
	got := r.finish(t, out)
	if got.value != "closed" || r.m.tuiInst.ActiveOverlay() != nil || component.disposed.Load() != 1 {
		t.Fatalf("Custom = %v; overlay %v; disposed %d", got.value, r.m.tuiInst.ActiveOverlay(), component.disposed.Load())
	}
}

// A cancelled context ends the call, restores the editor and disposes the component.
func TestExtUIContextCustomEndsWithItsContext(t *testing.T) {
	r := newCustomRig(t)
	component := &customProbe{label: "C"}
	ctx, cancel := context.WithCancel(t.Context())
	out := r.start(ctx, extension.CustomFactory(func(extension.TUI, *tui.Theme, extension.KeybindingsManager, func(any)) (extension.DisposableComponent, error) {
		return component, nil
	}), nil)
	r.pump(t, out, func() bool { return any(r.m.tuiInst.GetFocusedComponent()) == component })
	cancel()
	got := r.finish(t, out)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v", got.err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for component.disposed.Load() == 0 || any(r.m.tuiInst.GetFocusedComponent()) != any(r.m.editor) {
		select {
		case fn := <-r.m.uiTaskCh:
			fn()
		case <-time.After(time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup incomplete: disposed %d", component.disposed.Load())
		}
	}
}

// Pi types the factory, so a missing one is the only invalid value; a subprocess extension goes through RunRemoteOverlay instead.
func TestExtUIContextCustomRejectsAMissingFactory(t *testing.T) {
	r := newCustomRig(t)
	if _, err := r.u.Custom(t.Context(), nil, nil); err == nil {
		t.Fatal("Custom accepted nil")
	}
}

// interactive-mode.ts:2902-2905: overlayOptions given as a function is called when the overlay is shown, and onHandle receives the overlay's handle
// at once, so the extension can hide, focus or unfocus the overlay.
func TestExtUIContextCustomOverlayOptionsFunctionAndOnHandle(t *testing.T) {
	r := newCustomRig(t)
	component := &customProbe{label: "OVERLAY"}
	var optionCalls int
	var handle *tui.OverlayHandle
	var done func(any)
	out := r.start(t.Context(), extension.CustomFactory(func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return component, nil
	}), &extension.CustomOptions{
		Overlay: true,
		OverlayOptions: extension.OverlayOptionsFunc(func() tui.OverlayOptions {
			optionCalls++
			return tui.OverlaySpec{}.Options()
		}),
		OnHandle: func(h *tui.OverlayHandle) { handle = h },
	})
	r.pump(t, out, func() bool { return r.m.tuiInst.ActiveOverlay() != nil })
	if optionCalls != 1 {
		t.Fatalf("overlayOptions function called %d times, want once at show", optionCalls)
	}
	if handle == nil {
		t.Fatal("onHandle was not called with the overlay handle")
	}
	handle.Hide()
	if r.m.tuiInst.ActiveOverlay() != nil {
		t.Fatal("hiding through the onHandle handle left the overlay shown")
	}
	done("closed")
	r.finish(t, out)
}

// pastedDraft is a bracketed paste of twelve lines: the editor collapses it into a marker whose content only the editor can expand.
func pastedDraft(label string) string {
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s %d", label, i+1)
	}
	return strings.Join(lines, "\n")
}

func pasteInto(t *testing.T, editor *tui.Editor, text string) {
	t.Helper()
	editor.HandleInput("\x1b[200~" + text + "\x1b[201~")
	if editor.Text() == text {
		t.Fatal("the paste did not collapse into a marker; the transition under test would copy plain text")
	}
}

// pi main 51313540a test/interactive-mode-paste.test.ts "around non-overlay custom UI" (#9809; D251).
func TestExtUIContextCustomKeepsCollapsedPasteContent(t *testing.T) {
	t.Run("after it closes", func(t *testing.T) {
		r := newCustomRig(t)
		draft := pastedDraft("draft")
		pasteInto(t, r.m.editor, draft)
		var done func(any)
		out := r.start(t.Context(), extension.CustomFactory(func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
			done = d
			return &customProbe{label: "C"}, nil
		}), nil)
		r.pump(t, out, func() bool { return done != nil && r.m.editorContainer.Children()[0] != tui.Component(r.m.editor) })
		done(nil)
		r.finish(t, out)
		if got := r.m.editor.GetExpandedText(); got != draft {
			t.Fatalf("restored draft = %q, want %q", got, draft)
		}
	})
	t.Run("after it fails to open", func(t *testing.T) {
		r := newCustomRig(t)
		draft := pastedDraft("draft")
		pasteInto(t, r.m.editor, draft)
		out := r.start(t.Context(), extension.CustomFactory(func(extension.TUI, *tui.Theme, extension.KeybindingsManager, func(any)) (extension.DisposableComponent, error) {
			return nil, errors.New("factory failed")
		}), nil)
		r.finish(t, out)
		if got := r.m.editor.GetExpandedText(); got != draft {
			t.Fatalf("restored draft = %q, want %q", got, draft)
		}
	})
}

// pi main 51313540a "when queued messages are restored above it" (D251).
func TestRestoreQueuedMessagesKeepsCollapsedPasteContent(t *testing.T) {
	m := statusBorderMode(t, false)
	draft := pastedDraft("draft")
	pasteInto(t, m.editor, draft)
	m.agent.FollowUp(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "queued follow-up"}}}})
	m.restoreQueuedMessagesToEditor(false)
	if got, want := m.editor.GetExpandedText(), "queued follow-up\n\n"+draft; got != want {
		t.Fatalf("editor = %q, want %q", got, want)
	}
}

// pi main 51313540a "between the default and a replacement editor" (D251).
func TestSetRemoteEditorKeepsCollapsedPasteContent(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	draft := pastedDraft("into replacement")
	pasteInto(t, mode.editor, draft)
	custom := &fakeRemoteEditor{}
	mode.setRemoteEditor(custom)
	if !slices.Contains(custom.calls, "setText:"+draft) {
		t.Fatalf("replacement editor calls = %q, want setText of the expanded draft", custom.calls)
	}
	// And back: a draft pasted into the replacement editor, which reports its expanded text, returns to the default editor whole.
	backToDefault := pastedDraft("back to default")
	custom.host.EditorChanged("[paste #7 +12 lines]", backToDefault)
	runPostedTasks(t, mode)
	mode.setRemoteEditor(nil)
	if got := mode.editor.GetExpandedText(); got != backToDefault {
		t.Fatalf("default editor draft = %q, want %q", got, backToDefault)
	}
}

// pi main 51313540a "between the default and a replacement editor", the second half: a draft pasted into the replacement and carried back to the
// default editor by setCustomEditorComponent(undefined) submits intact (D251). The replacement reports its text with the marker and the expanded paste.
func TestSetRemoteEditorBackToTheDefaultKeepsCollapsedPasteContent(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	custom := &fakeRemoteEditor{}
	mode.setRemoteEditor(custom)
	draft := pastedDraft("back to default")
	custom.host.EditorChanged("[paste #1 +12 lines]", draft)
	runPostedTasks(t, mode)
	if got := mode.editor.GetExpandedText(); got != draft {
		t.Fatalf("draft in the replacement = %q, want %q", got, draft)
	}
	mode.setRemoteEditor(nil)
	if got := mode.editor.GetExpandedText(); got != draft {
		t.Fatalf("draft back in the default editor = %q, want %q", got, draft)
	}
}
