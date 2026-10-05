package tui

import "testing"

// Pi's cancel aborts the login's signal before it rejects the pending input (login-dialog.ts:86-93 in Pi 1.0.3). A
// prompt that sees its input end must therefore already find the login cancelled; otherwise the login reports a
// failure instead of a cancellation.
func TestLoginDialogCancelAbortsBeforeEndingTheInput(t *testing.T) {
	var input <-chan string
	inputOpenAtCancel := false
	dialog := NewLoginDialog("Cancel Order", func() {
		select {
		case _, ok := <-input:
			inputOpenAtCancel = ok
		default:
			inputOpenAtCancel = true
		}
	})
	input = dialog.ShowInput("Code:", "")
	dialog.HandleInput("\x1b")
	if !dialog.Cancelled() {
		t.Fatal("escape did not cancel the dialog")
	}
	if !inputOpenAtCancel {
		t.Fatal("the pending input ended before the login was cancelled")
	}
	if _, ok := <-input; ok {
		t.Fatal("the pending input was not ended after the cancel")
	}
}
