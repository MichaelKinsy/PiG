package codingagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
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

// Pi 1.2.0 interactive-mode.ts:2690-2857: showExtensionSelector, showExtensionInput and showExtensionEditor never refuse a
// dialog. Each clears the editor slot and installs itself, so a dialog that opens while another is shown displaces it: the
// new dialog takes the keys, and the displaced dialog's promise stays pending until its own signal aborts it.
func TestExtensionDialogDisplacesTheOpenDialog(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	ui := &ExtUIContext{m: m}
	ctxA, abortA := context.WithCancel(context.Background())
	defer abortA()

	first := startDialog(t, m, func() (string, error) {
		return ui.Select(ctxA, "First", []string{"one", "two"}, extension.ExtensionUIDialogOptions{})
	})
	second := startDialog(t, m, func() (string, error) {
		return ui.Input(context.Background(), "Second", "", extension.ExtensionUIDialogOptions{})
	})
	requirePending(t, second, "displacing input")
	requirePending(t, first, "displaced selector")
	if m.extensionDialog == nil {
		t.Fatal("displacing input is not installed")
	}
	if _, ok := m.extensionDialog.component.(*tui.ExtensionInputComponent); !ok {
		t.Fatalf("focused dialog = %T, want the displacing input", m.extensionDialog.component)
	}

	for _, key := range []string{"x", "\r"} {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	if got := requireResult(t, second, "displacing input"); got.err != nil || got.value != "x" {
		t.Fatalf("input = %q, %v; want x", got.value, got.err)
	}
	if m.extensionDialog != nil || len(m.editorContainer.Children()) != 1 || m.editorContainer.Children()[0] != m.editor {
		t.Fatal("submitted input did not restore the editor")
	}
	requirePending(t, first, "displaced selector")

	abortA()
	if got := requireResult(t, first, "displaced selector"); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("aborted selector = %q, %v; want cancellation", got.value, got.err)
	}
}

// Pi's abort listener calls hideExtensionSelector (interactive-mode.ts:2702-2705, 2737-2745), which restores the editor
// whatever dialog is shown, so aborting a displaced selector also hides the dialog that displaced it.
func TestAbortingADisplacedDialogHidesTheShownDialog(t *testing.T) {
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
	if got := requireResult(t, first, "displaced selector"); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("aborted selector = %q, %v; want cancellation", got.value, got.err)
	}
	pumpUITask(t, m, "abort did not post its hide to the UI loop")
	if m.extensionDialog != nil || m.editorContainer.Children()[0] != m.editor {
		t.Fatal("aborting the displaced selector left the newer selector shown")
	}
	requirePending(t, second, "hidden selector")
}
