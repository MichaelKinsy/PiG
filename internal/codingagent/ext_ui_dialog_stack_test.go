package codingagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi bdd9f039 (38847563, #11166, #9909) interactive-mode.ts pushExtensionDialog/removeExtensionDialog/showTopExtensionDialog:
// select, confirm, input, editor and an editor-slot custom component form a stack in the editor slot. A new dialog hides the
// visible one without settling it, closing a dialog shows the previous one again, and an abort removes only its own dialog.
// The first four cases port packages/coding-agent/test/interactive-mode-extension-dialogs.test.ts; the rest cover the PiG parts
// the stack also owns: the transcript cap, terminal focus of a subprocess component, the countdown timeout and the replacement
// reset.

// openExtensionDialogs is the number of dialogs the editor slot stack holds.
func openExtensionDialogs(m *InteractiveMode) int { return len(m.extensionDialogs) }

// callDialog runs call on a worker and pumps the owner loop until it has opened one more dialog.
func (r *customRig) callDialog(t *testing.T, call func() (string, error)) <-chan dialogCall {
	t.Helper()
	before := openExtensionDialogs(r.m)
	result := make(chan dialogCall, 1)
	r.workers.Go(func() {
		value, err := call()
		result <- dialogCall{value, err}
	})
	r.pump(t, nil, func() bool { return openExtensionDialogs(r.m) > before })
	return result
}

func (r *customRig) confirmCall(ctx context.Context, title, message string) func() (string, error) {
	return func() (string, error) {
		confirmed, err := r.u.Confirm(ctx, title, message, extension.ExtensionUIDialogOptions{})
		return fmt.Sprint(confirmed), err
	}
}

func (r *customRig) selectCall(ctx context.Context, title string, options ...string) func() (string, error) {
	return func() (string, error) {
		return r.u.Select(ctx, title, options, extension.ExtensionUIDialogOptions{})
	}
}

// requireVisible checks that the editor slot holds exactly the focused component and that it shows text.
func requireVisible(t *testing.T, m *InteractiveMode, text string) {
	t.Helper()
	children := m.editorContainer.Children()
	if len(children) != 1 || m.tuiInst.GetFocusedComponent() != children[0] {
		t.Fatalf("editor slot = %v, focus = %T; want one component that has the focus", children, m.tuiInst.GetFocusedComponent())
	}
	if got := widthx.StripAnsi(strings.Join(children[0].Render(80), "\n")); !strings.Contains(got, text) {
		t.Fatalf("visible component renders %q, want it to show %q", got, text)
	}
}

func requireEditorShown(t *testing.T, m *InteractiveMode, what string) {
	t.Helper()
	children := m.editorContainer.Children()
	if len(children) != 1 || children[0] != tui.Component(m.editor) {
		t.Fatalf("editor slot %s = %v, want the editor", what, children)
	}
	requireFocus(t, m, m.editor, what)
	if n := openExtensionDialogs(m); n != 0 {
		t.Fatalf("%d dialogs are left open %s", n, what)
	}
}

// requireBlocked checks the status the program reported last: blocked with kind and message, or idle for an empty kind.
func requireBlocked(t *testing.T, terminal *programStatusTerminal, kind tui.ProgramStatusKind, message string) {
	t.Helper()
	if len(terminal.reports) == 0 {
		t.Fatal("the program reported no status")
	}
	got := terminal.reports[len(terminal.reports)-1]
	want := tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: kind, Message: message}
	if kind == "" {
		want = tui.ProgramStatus{State: tui.ProgramStateIdle}
	}
	got.App = ""
	if got != want {
		t.Fatalf("last program status = %+v, want %+v", got, want)
	}
}

// interactive-mode-extension-dialogs.test.ts "shows the earlier confirm again after the later one is answered" (#11166).
func TestExtensionDialogsShowTheEarlierConfirmAgainAfterTheLaterIsAnswered(t *testing.T) {
	r := newCustomRig(t)
	terminal := recordProgramStatus(r.m, "")
	first := r.callDialog(t, r.confirmCall(r.m.runCtx, "First", "first dialog"))
	second := r.callDialog(t, r.confirmCall(r.m.runCtx, "Second", "second dialog"))

	requireVisible(t, r.m, "Second")
	requireBlocked(t, terminal, tui.ProgramStatusKindPermission, "Second")
	requirePending(t, first, "the hidden confirm")
	if err := r.m.dispatchKey(t.Context(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, second, "the later confirm"); got.value != "false" || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the later confirm = %q, %v; want it declined", got.value, got.err)
	}

	requireVisible(t, r.m, "First")
	requireBlocked(t, terminal, tui.ProgramStatusKindPermission, "First")
	requirePending(t, first, "the earlier confirm")
	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, first, "the earlier confirm"); got.value != "true" || got.err != nil {
		t.Fatalf("the earlier confirm = %q, %v; want it accepted", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after both confirms")
	requireBlocked(t, terminal, "", "")
}

// interactive-mode-extension-dialogs.test.ts "removes a hidden dialog when its signal aborts": the abort of the dialog below the
// visible one settles that one only.
func TestExtensionDialogsRemoveAHiddenDialogWhenItsSignalAborts(t *testing.T) {
	r := newCustomRig(t)
	ctxFirst, abortFirst := context.WithCancel(t.Context())
	defer abortFirst()
	first := r.callDialog(t, r.confirmCall(ctxFirst, "First", "first dialog"))
	second := r.callDialog(t, r.confirmCall(r.m.runCtx, "Second", "second dialog"))

	abortFirst()
	if got := requireResult(t, first, "the aborted confirm"); got.value != "false" || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the aborted confirm = %q, %v; want it cancelled", got.value, got.err)
	}
	r.pump(t, nil, func() bool { return openExtensionDialogs(r.m) == 1 })
	requireVisible(t, r.m, "Second")
	requirePending(t, second, "the visible confirm")

	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, second, "the visible confirm"); got.value != "true" || got.err != nil {
		t.Fatalf("the visible confirm = %q, %v; want it accepted", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after the visible confirm")
}

// startCustom opens an editor-slot custom component and returns its probe, done callback and outcome.
func (r *customRig) startCustom(t *testing.T, label string) (*customProbe, *func(any), <-chan customOutcome) {
	t.Helper()
	before := openExtensionDialogs(r.m)
	component := &customProbe{label: label}
	done := new(func(any))
	out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		*done = d
		return component, nil
	}, nil)
	r.pump(t, out, func() bool { return openExtensionDialogs(r.m) > before })
	return component, done, out
}

// interactive-mode-extension-dialogs.test.ts "stacks concurrent inline custom components" (#9909).
func TestExtensionDialogsStackConcurrentInlineCustomComponents(t *testing.T) {
	r := newCustomRig(t)
	_, doneFirst, first := r.startCustom(t, "custom one")
	_, doneSecond, second := r.startCustom(t, "custom two")
	requireVisible(t, r.m, "custom two")

	go (*doneSecond)("two")
	if got := r.finish(t, second); got.err != nil || got.value != "two" {
		t.Fatalf("the second custom = %v, %v; want two", got.value, got.err)
	}
	requireVisible(t, r.m, "custom one")

	go (*doneFirst)("one")
	if got := r.finish(t, first); got.err != nil || got.value != "one" {
		t.Fatalf("the first custom = %v, %v; want one", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after both custom components")
}

// formProbe is a custom component whose HandleInput opens a dialog, as an extension's input handler does.
type formProbe struct {
	customProbe
	onInput func()
}

func (f *formProbe) HandleInput(data string) {
	f.customProbe.HandleInput(data)
	f.onInput()
}

// interactive-mode-extension-dialogs.test.ts "shows a confirm opened from an inline custom component and then restores the
// component".
func TestExtensionDialogsShowAConfirmOpenedFromACustomComponentThenRestoreIt(t *testing.T) {
	r := newCustomRig(t)
	var confirmed <-chan dialogCall
	var done func(any)
	form := &formProbe{customProbe: customProbe{label: "custom form"}}
	form.onInput = func() {
		result := make(chan dialogCall, 1)
		confirmed = result
		r.workers.Go(func() {
			value, err := r.confirmCall(r.m.runCtx, "Discard?", "discard changes")()
			result <- dialogCall{value, err}
		})
	}
	out := r.start(t.Context(), func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, d func(any)) (extension.DisposableComponent, error) {
		done = d
		return form, nil
	}, nil)
	r.pump(t, out, func() bool { return openExtensionDialogs(r.m) == 1 })
	requireVisible(t, r.m, "custom form")

	if err := r.m.dispatchKey(t.Context(), "x"); err != nil {
		t.Fatal(err)
	}
	r.pump(t, out, func() bool { return openExtensionDialogs(r.m) == 2 })
	requireVisible(t, r.m, "Discard?")
	if err := r.m.dispatchKey(t.Context(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, confirmed, "the confirm"); got.value != "false" || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the confirm = %q, %v; want it declined", got.value, got.err)
	}

	requireVisible(t, r.m, "custom form")
	go done("kept")
	if got := r.finish(t, out); got.err != nil || got.value != "kept" {
		t.Fatalf("the custom component = %v, %v; want kept", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after the custom component")
}

// A select, an input and an editor opened together are all reachable: the last takes the keys, and each answer settles its own
// call while the previous dialog comes back. The transcript cap follows the visible dialog.
func TestExtensionDialogsAnswerASelectInputAndEditorMixInStackOrder(t *testing.T) {
	r := newCustomRig(t)
	terminal := recordProgramStatus(r.m, "")
	for i := range 60 {
		r.m.chatContainer.Add(tui.NewText(fmt.Sprintf("line %d", i)))
	}
	pickCall := r.callDialog(t, r.selectCall(r.m.runCtx, "Pick", "one", "two"))
	inputCall := r.callDialog(t, func() (string, error) {
		return r.u.Input(r.m.runCtx, "Name", "", extension.ExtensionUIDialogOptions{})
	})
	editCall := r.callDialog(t, func() (string, error) { return r.u.Editor(r.m.runCtx, "Note", "pre") })
	requireVisible(t, r.m, "Note")
	requireBlocked(t, terminal, tui.ProgramStatusKindQuestion, "Note")
	if len(r.m.chatContainer.Render(100)) == 60 {
		t.Fatal("the visible dialog did not cap the transcript")
	}

	for _, key := range []string{"-x", "\r"} {
		if err := r.m.dispatchKey(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if got := requireResult(t, editCall, "the editor"); got.value != "pre-x" || got.err != nil {
		t.Fatalf("the editor = %q, %v; want pre-x", got.value, got.err)
	}
	requireVisible(t, r.m, "Name")
	requireBlocked(t, terminal, tui.ProgramStatusKindQuestion, "Name")
	requirePending(t, inputCall, "the hidden input")
	requirePending(t, pickCall, "the hidden select")

	for _, key := range []string{"Ada", "\r"} {
		if err := r.m.dispatchKey(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if got := requireResult(t, inputCall, "the input"); got.value != "Ada" || got.err != nil {
		t.Fatalf("the input = %q, %v; want Ada", got.value, got.err)
	}
	requireVisible(t, r.m, "Pick")
	requireBlocked(t, terminal, tui.ProgramStatusKindQuestion, "Pick")
	if len(r.m.chatContainer.Render(100)) == 60 {
		t.Fatal("the select under the answered dialogs lost the transcript cap")
	}

	for _, key := range []string{"\x1b[B", "\r"} {
		if err := r.m.dispatchKey(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if got := requireResult(t, pickCall, "the select"); got.value != "two" || got.err != nil {
		t.Fatalf("the select = %q, %v; want two", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after the last dialog")
	if got := len(r.m.chatContainer.Render(100)); got != 60 {
		t.Fatalf("the transcript shows %d lines after the last dialog, want all 60", got)
	}
}

// A dialog over an editor-slot custom component hides it and takes the keys: the component gets none, it is shown again when the
// dialog closes, and a component that ends while the dialog is shown leaves the dialog alone and restores the editor text it saved.
func TestExtensionDialogOverACustomComponentLeavesTheDialogShownWhenTheComponentEnds(t *testing.T) {
	r := newCustomRig(t)
	r.m.editor.SetText("draft")
	component, done, out := r.startCustom(t, "custom")
	selected := r.callDialog(t, r.selectCall(r.m.runCtx, "Pick", "one"))
	requireVisible(t, r.m, "Pick")
	if err := r.m.dispatchKey(t.Context(), "x"); err != nil {
		t.Fatal(err)
	}
	if got := component.received(); len(got) != 0 {
		t.Fatalf("the hidden custom component received %q", got)
	}
	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, selected, "the select"); got.value != "one" || got.err != nil {
		t.Fatalf("the select = %q, %v; want one", got.value, got.err)
	}
	requireVisible(t, r.m, "custom")

	selected = r.callDialog(t, r.selectCall(r.m.runCtx, "Again", "one"))
	r.m.editor.SetText("typed while open")
	go (*done)("closed underneath")
	if got := r.finish(t, out); got.err != nil || got.value != "closed underneath" {
		t.Fatalf("the custom component = %v, %v", got.value, got.err)
	}
	requireVisible(t, r.m, "Again")
	if got := r.m.editor.Text(); got != "draft" {
		t.Fatalf("editor text after the hidden component ended = %q, want the text it saved", got)
	}
	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, selected, "the second select"); got.value != "one" || got.err != nil {
		t.Fatalf("the second select = %q, %v; want one", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after the dialog")
}

// The countdown of a hidden dialog expires like any other: it settles that dialog and leaves the visible one.
func TestExtensionDialogTimeoutOfAHiddenDialogLeavesTheVisibleDialog(t *testing.T) {
	r := newCustomRig(t)
	timed := r.callDialog(t, func() (string, error) {
		return r.u.Select(r.m.runCtx, "Timed", []string{"one"}, extension.ExtensionUIDialogOptions{Timeout: new(float64(1000))})
	})
	visible := r.callDialog(t, r.selectCall(r.m.runCtx, "Visible", "one"))
	deadline := time.After(5 * time.Second)
	for openExtensionDialogs(r.m) != 1 {
		select {
		case task := <-r.m.uiTaskCh:
			task()
		case <-r.m.renderWakeCh:
			r.m.runScheduledRender()
		case <-deadline:
			t.Fatal("the hidden dialog did not expire")
		}
	}
	if got := requireResult(t, timed, "the expired dialog"); got.value != "" || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the expired dialog = %q, %v; want cancellation", got.value, got.err)
	}
	requireVisible(t, r.m, "Visible")
	requirePending(t, visible, "the visible dialog")
	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, visible, "the visible dialog"); got.value != "one" || got.err != nil {
		t.Fatalf("the visible dialog = %q, %v; want one", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after the visible dialog")
}

// A Session replacement resets the extension UI: interactive-mode.ts resetExtensionUI disposes every dialog and shows the editor. A
// call behind a discarded dialog that ends later leaves a dialog opened since alone.
func TestSessionReplacementDisposesEveryExtensionDialog(t *testing.T) {
	r := newCustomRig(t)
	terminal := recordProgramStatus(r.m, "")
	component, _, out := r.startCustom(t, "custom")
	ctxOld, abortOld := context.WithCancel(t.Context())
	defer abortOld()
	old := r.callDialog(t, r.selectCall(ctxOld, "Old", "one"))
	requireVisible(t, r.m, "Old")

	r.onWorker(t, r.m.resetExtensionUIForReplacement)
	requireEditorShown(t, r.m, "after the replacement reset")
	requireBlocked(t, terminal, "", "")
	if got := component.disposed.Load(); got != 1 {
		t.Fatalf("custom component disposed %d times by the reset, want once", got)
	}

	fresh := r.callDialog(t, r.selectCall(r.m.runCtx, "Fresh", "one"))
	abortOld()
	if got := requireResult(t, old, "the discarded dialog"); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the discarded dialog = %q, %v; want cancellation", got.value, got.err)
	}
	r.pump(t, nil, func() bool { return openExtensionDialogs(r.m) == 1 })
	requireVisible(t, r.m, "Fresh")
	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, fresh, "the fresh dialog"); got.value != "one" || got.err != nil {
		t.Fatalf("the fresh dialog = %q, %v; want one", got.value, got.err)
	}
	select {
	case got := <-out:
		t.Fatalf("the custom call ended with %v, %v although the reset only disposes it", got.value, got.err)
	default:
	}
}

// A dialog over a subprocess extension's editor-slot component takes the terminal from it: the component learns it lost focus and
// stops reading, then reads again, with focus published to it, once the dialog closes. A component that ends while hidden leaves
// the dialog shown.
func TestExtensionDialogOverARemoteEditorSlotComponentHandsFocusBack(t *testing.T) {
	r := newCustomRig(t)
	slot := r.openRemote(t, "SLOT", false)
	requireFocus(t, r.m, slot.handle.(*customOverlay), "with the editor-slot component shown")
	selected := r.callDialog(t, r.selectCall(r.m.runCtx, "Pick", "one", "two"))
	requireVisible(t, r.m, "Pick")
	slot.requireFocusEvents(t, r, false)
	if slot.reading() {
		t.Fatal("the component still reads the terminal under the dialog")
	}
	if got := r.routeKey(t, "\x1b[B", slot); got != "main" {
		t.Fatalf("key under the dialog went to %s, want the main loop", got)
	}
	if got := r.routeKey(t, "\r", slot); got != "main" {
		t.Fatalf("key under the dialog went to %s, want the main loop", got)
	}
	if got := requireResult(t, selected, "the select"); got.value != "two" || got.err != nil {
		t.Fatalf("the select = %q, %v; want two", got.value, got.err)
	}
	requireFocus(t, r.m, slot.handle.(*customOverlay), "after the dialog closed")
	slot.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "a", slot); got != "SLOT" {
		t.Fatalf("key after the dialog closed went to %s, want SLOT", got)
	}

	selected = r.callDialog(t, r.selectCall(r.m.runCtx, "Again", "one"))
	slot.requireFocusEvents(t, r, false)
	slot.close(t, r, "closed underneath")
	requireVisible(t, r.m, "Again")
	if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, selected, "the second select"); got.value != "one" || got.err != nil {
		t.Fatalf("the second select = %q, %v; want one", got.value, got.err)
	}
	requireEditorShown(t, r.m, "after the dialog")
}

// Two subprocess editor-slot components stack: the second hides the first, which stops reading and learns it lost focus, and
// closing the second shows the first again with focus published back.
func TestRemoteEditorSlotComponentsStackAndHandFocusBack(t *testing.T) {
	r := newCustomRig(t)
	back := r.openRemote(t, "BACK", false)
	front := r.openRemote(t, "FRONT", false)
	requireFocus(t, r.m, front.handle.(*customOverlay), "with two components shown")
	if back.reading() || !front.reading() {
		t.Fatalf("terminal readers: back=%t front=%t; want only the visible front component", back.reading(), front.reading())
	}
	back.requireFocusEvents(t, r, false)
	front.requireFocusEvents(t, r)
	if got := r.routeKey(t, "a", back, front); got != "FRONT" {
		t.Fatalf("key with two components shown went to %s, want FRONT", got)
	}
	front.close(t, r, "front")
	requireFocus(t, r.m, back.handle.(*customOverlay), "after the front component closed")
	back.requireFocusEvents(t, r, true)
	if got := r.routeKey(t, "b", back); got != "BACK" {
		t.Fatalf("key after the front component closed went to %s, want BACK", got)
	}
	back.close(t, r, "back")
	requireEditorShown(t, r.m, "after both components")
}

// A custom call that fails to mount (its factory errors, or calls done before it returns) while another dialog is shown must not clear that
// dialog: Pi's restoreEditor with no component mounted only puts the editor text back (interactive-mode.ts showExtensionCustom).
func TestExtensionCustomThatNeverMountsLeavesTheShownDialog(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factory extension.CustomFactory
		want    func(t *testing.T, got customOutcome)
	}{
		{"factory error", func(extension.TUI, *tui.Theme, extension.KeybindingsManager, func(any)) (extension.DisposableComponent, error) {
			return nil, errors.New("boom")
		}, func(t *testing.T, got customOutcome) {
			if got.err == nil {
				t.Fatal("a factory error did not reject the call")
			}
		}},
		{"done before the factory returns", func(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, done func(any)) (extension.DisposableComponent, error) {
			done("early")
			return &customProbe{label: "NEVER"}, nil
		}, func(t *testing.T, got customOutcome) {
			if got.err != nil || got.value != "early" {
				t.Fatalf("custom = %v, %v; want early", got.value, got.err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCustomRig(t)
			r.m.editor.SetText("draft")
			selected := r.callDialog(t, r.selectCall(r.m.runCtx, "Pick", "one"))
			requireVisible(t, r.m, "Pick")
			r.m.editor.SetText("typed while the dialog is open")
			out := r.start(t.Context(), tc.factory, nil)
			tc.want(t, r.finish(t, out))
			requireVisible(t, r.m, "Pick")
			if n := openExtensionDialogs(r.m); n != 1 {
				t.Fatalf("%d dialogs are open, want the select alone", n)
			}
			if err := r.m.dispatchKey(t.Context(), "\r"); err != nil {
				t.Fatal(err)
			}
			if got := requireResult(t, selected, "the select"); got.value != "one" || got.err != nil {
				t.Fatalf("the select = %q, %v; want one", got.value, got.err)
			}
			requireEditorShown(t, r.m, "after the select")
		})
	}
}

// Pi's handleReloadCommand calls resetExtensionUI before the reload box shows, which disposes every stacked dialog; a dialog the reload left
// in the stack would come back over the editor later.
func TestReloadDisposesEveryExtensionDialog(t *testing.T) {
	r := newCustomRig(t)
	ctx, abort := context.WithCancel(t.Context())
	defer abort()
	r.callDialog(t, r.selectCall(ctx, "Old", "one"))
	component, _, _ := r.startCustom(t, "custom")
	requireVisible(t, r.m, "custom")

	if err := r.m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	requireEditorShown(t, r.m, "after /reload")
	if got := component.disposed.Load(); got != 1 {
		t.Fatalf("custom component disposed %d times by the reload, want once", got)
	}
}

// A subprocess component in the editor slot restores the editor text saved when it opened, as the in-process one does (restoreEditor
// setText(savedText)).
func TestRemoteEditorSlotComponentRestoresTheEditorText(t *testing.T) {
	r := newCustomRig(t)
	r.m.editor.SetText("draft")
	slot := r.openRemote(t, "SLOT", false)
	r.m.editor.SetText("changed while the component was open")
	slot.close(t, r, "done")
	if got := r.m.editor.Text(); got != "draft" {
		t.Fatalf("editor text after the component closed = %q, want the text saved at open", got)
	}
}
