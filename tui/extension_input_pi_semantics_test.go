package tui

import "testing"

// extension-input.ts:61-76 handleInput: confirm submits the input's value and cancel cancels, with the callbacks the host gave; Pig's host-polled state is
// completed once, so input after an answer is ignored and a late Cancel or countdown expiry does not turn a submitted input into a cancelled one.
// mutation-checked: dropping the done guard in HandleInput, Cancel or expire fails.
func TestExtensionInputComponentIgnoresInputAndCancelAfterItIsAnswered(t *testing.T) {
	submits := 0
	cancels := 0
	in := NewExtensionInputComponent("Name", "", func(string) { submits++ }, func() { cancels++ })
	if _, ok := any(in).(*ExtensionInputComponent); !ok {
		t.Fatalf("NewExtensionInputComponent returned %T", in)
	}
	in.HandleInput("a")
	in.HandleInput("\r")
	if !in.Done() || in.Cancelled() || submits != 1 || in.Text() != "a" {
		t.Fatalf("after submit: done %v cancelled %v submits %d text %q", in.Done(), in.Cancelled(), submits, in.Text())
	}
	in.HandleInput("b")
	in.HandleInput("\r")
	if in.Text() != "a" || submits != 1 {
		t.Fatalf("an answered input reacted to input: text %q, submits %d", in.Text(), submits)
	}
	in.Cancel()
	in.expire()
	if in.Cancelled() || cancels != 0 {
		t.Fatalf("a late Cancel or expiry cancelled a submitted input: cancelled %v, callbacks %d", in.Cancelled(), cancels)
	}
	open := NewExtensionInputComponent("Name", "", nil, func() { cancels++ })
	open.expire()
	if !open.Cancelled() || cancels != 1 {
		t.Fatalf("expiry of an open input: cancelled %v, callbacks %d, want true and 1", open.Cancelled(), cancels)
	}
}

// extension-input.ts:61-66 handleInput with no callbacks (the host polls Done and Cancelled): confirm and cancel finish the input without calling anything, and
// every handled key invalidates the component.
// mutation-checked: a nil submit callback that is called anyway, or HandleInput not invalidating, fails.
func TestExtensionInputComponentWorksWithoutCallbacksAndInvalidatesOnInput(t *testing.T) {
	submitOnly := NewExtensionInputComponent("Name", "", nil, nil)
	submitOnly.HandleInput("\r")
	if !submitOnly.Done() || submitOnly.Cancelled() {
		t.Fatalf("confirm without callbacks: done %v cancelled %v", submitOnly.Done(), submitOnly.Cancelled())
	}
	cancelOnly := NewExtensionInputComponent("Name", "", nil, nil)
	cancelOnly.HandleInput("\x1b")
	if !cancelOnly.Done() || !cancelOnly.Cancelled() {
		t.Fatalf("cancel without callbacks: done %v cancelled %v", cancelOnly.Done(), cancelOnly.Cancelled())
	}
	typing := NewExtensionInputComponent("Name", "", nil, nil)
	typing.NeedsRedraw()
	typing.HandleInput("x")
	if !typing.IsDirty() || typing.Text() != "x" {
		t.Fatalf("typed key: dirty %v text %q, want the component invalidated and the text delegated", typing.IsDirty(), typing.Text())
	}
}
