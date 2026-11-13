package codingagent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type dialogCall struct {
	value string
	err   error
}

func startDialog(t *testing.T, m *InteractiveMode, call func() (string, error)) <-chan dialogCall {
	t.Helper()
	result := make(chan dialogCall, 1)
	go func() {
		value, err := call()
		result <- dialogCall{value, err}
	}()
	pumpUITask(t, m, "dialog did not post its installation to the UI loop")
	return result
}

func pumpUITask(t *testing.T, m *InteractiveMode, failure string) {
	t.Helper()
	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

func requirePending(t *testing.T, result <-chan dialogCall, name string) {
	t.Helper()
	select {
	case got := <-result:
		t.Fatalf("%s resolved with %q, %v; want it pending", name, got.value, got.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func requireResult(t *testing.T, result <-chan dialogCall, name string) dialogCall {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(time.Second):
		t.Fatalf("%s did not resolve", name)
		return dialogCall{}
	}
}

// Pi bdd9f039 interactive-mode.ts pushExtensionDialog (#11166): a dialog that opens while another is shown hides it without
// settling it. The new dialog takes the keys, and once it closes the earlier dialog shows again and stays answerable.
func TestExtensionDialogStacksOverTheOpenDialog(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	ui := &ExtUIContext{m: m}

	first := startDialog(t, m, func() (string, error) {
		return ui.Select(context.Background(), "First", []string{"one", "two"}, extension.ExtensionUIDialogOptions{})
	})
	second := startDialog(t, m, func() (string, error) {
		return ui.Input(context.Background(), "Second", "", extension.ExtensionUIDialogOptions{})
	})
	requirePending(t, second, "stacked input")
	requirePending(t, first, "hidden selector")
	shown := m.shownExtensionDialog()
	if shown == nil {
		t.Fatal("stacked input is not installed")
	}
	if _, ok := shown.component.(*tui.ExtensionInputComponent); !ok {
		t.Fatalf("focused dialog = %T, want the stacked input", shown.component)
	}

	for _, key := range []string{"x", "\r"} {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	if got := requireResult(t, second, "stacked input"); got.err != nil || got.value != "x" {
		t.Fatalf("input = %q, %v; want x", got.value, got.err)
	}
	shown = m.shownExtensionDialog()
	if shown == nil || len(m.editorContainer.Children()) != 1 || m.editorContainer.Children()[0] != shown.component {
		t.Fatal("submitted input did not show the hidden selector again")
	}
	if _, ok := shown.component.(*tui.ExtensionSelectorComponent); !ok {
		t.Fatalf("shown dialog = %T, want the earlier selector", shown.component)
	}
	requirePending(t, first, "hidden selector")

	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, first, "earlier selector"); got.err != nil || got.value != "one" {
		t.Fatalf("selector = %q, %v; want one", got.value, got.err)
	}
	if m.shownExtensionDialog() != nil || m.editorContainer.Children()[0] != tui.Component(m.editor) {
		t.Fatal("answering the last dialog did not restore the editor")
	}
}

// Pi's abort listener finishes its own dialog (interactive-mode.ts showExtensionSelector finish): aborting the hidden selector
// removes it from the stack and leaves the dialog that hides it shown.
func TestAbortingAHiddenDialogLeavesTheShownDialog(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	ui := &ExtUIContext{m: m}
	ctxA, abortA := context.WithCancel(context.Background())
	defer abortA()
	ctxB := t.Context()

	first := startDialog(t, m, func() (string, error) {
		return ui.Select(ctxA, "First", []string{"one"}, extension.ExtensionUIDialogOptions{})
	})
	second := startDialog(t, m, func() (string, error) {
		return ui.Select(ctxB, "Second", []string{"two"}, extension.ExtensionUIDialogOptions{})
	})
	abortA()
	if got := requireResult(t, first, "hidden selector"); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("aborted selector = %q, %v; want cancellation", got.value, got.err)
	}
	pumpUITask(t, m, "abort did not post its removal to the UI loop")
	shown := m.shownExtensionDialog()
	if shown == nil || m.editorContainer.Children()[0] != shown.component {
		t.Fatal("aborting the hidden selector removed the shown selector")
	}
	requirePending(t, second, "shown selector")
	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, second, "shown selector"); got.err != nil || got.value != "two" {
		t.Fatalf("shown selector = %q, %v; want two", got.value, got.err)
	}
	if m.shownExtensionDialog() != nil || m.editorContainer.Children()[0] != tui.Component(m.editor) {
		t.Fatal("answering the last dialog did not restore the editor")
	}
}

// pi main 3b57ed7e2 (#11072) through the extension dialog path: ui.select with a long list shows a ten-option window around the
// cursor, the cursor keys dispatched by the mode move it, and the chosen option settles the call.
func TestExtensionSelectLongListShowsAWindowAroundTheCursor(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	ui := &ExtUIContext{m: m}
	options := make([]string, 15)
	for i := range options {
		options[i] = "option-" + strconv.Itoa(i+1)
	}
	result := startDialog(t, m, func() (string, error) {
		return ui.Select(context.Background(), "Choose", options, extension.ExtensionUIDialogOptions{})
	})
	shown := m.shownExtensionDialog()
	if shown == nil {
		t.Fatal("selector is not installed")
	}
	rows := func() string { return widthx.StripAnsi(strings.Join(shown.component.Render(80), "\n")) }
	if out := rows(); !strings.Contains(out, "option-10") || strings.Contains(out, "option-11") || !strings.Contains(out, "  (1/15)") {
		t.Fatalf("first window:\n%s", out)
	}
	for range 14 {
		if err := m.dispatchKey(context.Background(), "\x1b[B"); err != nil {
			t.Fatal(err)
		}
	}
	if out := rows(); !strings.Contains(out, "→ option-15") || strings.Contains(out, "option-5\n") || !strings.Contains(out, "  (15/15)") {
		t.Fatalf("last window:\n%s", out)
	}
	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, result, "selector"); got.err != nil || got.value != "option-15" {
		t.Fatalf("selected %q, %v; want option-15", got.value, got.err)
	}
}
