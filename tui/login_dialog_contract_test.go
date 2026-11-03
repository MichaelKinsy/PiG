package tui

import (
	"sync"
	"testing"
	"testing/synctest"
)

// loginActionRecorder counts the clipboard writes and browser launches a login dialog makes. Each test runs in a synctest bubble, so synctest.Wait after a key
// returns only once the dialog's own clipboard goroutine has finished: "nothing was copied" is observed, not assumed.
type loginActionRecorder struct {
	mu     sync.Mutex
	copied []string
	opened []string
}

func (r *loginActionRecorder) copyText(text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.copied = append(r.copied, text)
	return nil
}

func (r *loginActionRecorder) open(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opened = append(r.opened, url)
}

func (r *loginActionRecorder) counts() (copied, opened []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.copied...), append([]string(nil), r.opened...)
}

// newRecordedLoginDialog is a focused dialog with real default bindings plus extra user bindings. completions counts onComplete calls.
func newRecordedLoginDialog(t *testing.T, bindings map[string][]string) (*LoginDialogComponent, *loginActionRecorder, *int) {
	t.Helper()
	previous := GetKeybindings()
	t.Cleanup(func() { SetKeybindings(previous) })
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), bindings))
	recorder := &loginActionRecorder{}
	completions := new(int)
	dialog := NewLoginDialogComponent(nil, "custom-provider", func(bool, string) { *completions++ }, "")
	dialog.SetCopyToClipboard(recorder.copyText, nil)
	dialog.SetOpenBrowser(recorder.open)
	dialog.SetFocused(true)
	return dialog, recorder, completions
}

func wantLoginActions(t *testing.T, r *loginActionRecorder, copied, opened int) {
	t.Helper()
	synctest.Wait()
	c, o := r.counts()
	if len(c) != copied || len(o) != opened {
		t.Fatalf("clipboard writes = %q, browser launches = %q; want %d and %d", c, o, copied, opened)
	}
}

// login-dialog.ts handleInput order: tui.select.cancel, then app.message.copy while an auth URL is shown, then the device-code confirm, then the prompt Input.
// Each pair that can be active together is bound to one key and the earlier branch must win; the branches' preconditions (a shown URL, a pending device
// action, no pending input, focus) are each broken in turn. The expectations come from login-dialog.ts:318-347.
func TestLoginDialogHandleInputBranchOrderAndPreconditions(t *testing.T) {
	device := OAuthDeviceCodeInfo{UserCode: "ABCD-EFGH", VerificationURI: "https://example.com/device"}
	t.Run("cancel wins over the copy binding", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, completions := newRecordedLoginDialog(t, map[string][]string{KBSelectCancel: {"ctrl+x"}, "app.message.copy": {"ctrl+x"}})
			dialog.ShowAuth("https://example.com/authorize", "")
			dialog.HandleInput("\x18")
			wantLoginActions(t, recorder, 0, 0)
			if *completions != 1 || !dialog.Cancelled() {
				t.Fatalf("completions=%d cancelled=%v, want 1 and true", *completions, dialog.Cancelled())
			}
		})
	})
	t.Run("copy binding copies the shown URL on every press and survives cancel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, completions := newRecordedLoginDialog(t, map[string][]string{"app.message.copy": {"ctrl+x"}})
			dialog.ShowAuth("https://example.com/authorize", "")
			dialog.HandleInput("\x18")
			dialog.HandleInput("\x18")
			wantLoginActions(t, recorder, 2, 0)
			dialog.HandleInput("\x1b")
			dialog.HandleInput("\x18")
			wantLoginActions(t, recorder, 3, 0)
			if *completions != 1 {
				t.Fatalf("completions = %d, want 1", *completions)
			}
		})
	})
	t.Run("copy binding copies nothing without a shown URL", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, _ := newRecordedLoginDialog(t, map[string][]string{"app.message.copy": {"ctrl+x"}})
			dialog.HandleInput("\x18")
			dialog.ShowAuth("https://example.com/authorize", "")
			dialog.ShowDetails([]string{"Choose a login method"})
			dialog.HandleInput("\x18")
			wantLoginActions(t, recorder, 0, 0)
		})
	})
	t.Run("the copy binding is consumed and never typed into a pending prompt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, _ := newRecordedLoginDialog(t, map[string][]string{"app.message.copy": {"x"}})
			dialog.ShowAuth("https://example.com/authorize", "")
			pending := dialog.ShowPrompt("Paste the code", "")
			dialog.HandleInput("x")
			wantLoginActions(t, recorder, 1, 0)
			dialog.HandleInput("\r")
			select {
			case value := <-pending:
				if value != "" {
					t.Fatalf("the prompt received %q, want the copy key to be consumed", value)
				}
			default:
				t.Fatal("the prompt did not resolve")
			}
		})
	})
	t.Run("cancel wins over the device-code confirm", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, completions := newRecordedLoginDialog(t, map[string][]string{KBSelectConfirm: {"ctrl+x"}, KBSelectCancel: {"ctrl+x"}})
			dialog.ShowDeviceCode(device)
			dialog.HandleInput("\x18")
			wantLoginActions(t, recorder, 0, 0)
			if *completions != 1 || !dialog.Cancelled() {
				t.Fatalf("completions=%d cancelled=%v, want 1 and true", *completions, dialog.Cancelled())
			}
		})
	})
	t.Run("the device-code confirm acts once and only on the configured key", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, _ := newRecordedLoginDialog(t, map[string][]string{KBSelectConfirm: {"ctrl+y"}})
			dialog.ShowDeviceCode(device)
			dialog.HandleInput("\r")
			wantLoginActions(t, recorder, 0, 0)
			dialog.HandleInput("\x19")
			dialog.HandleInput("\x19")
			wantLoginActions(t, recorder, 1, 1)
		})
	})
	t.Run("the device-code confirm waits while an input is pending", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, _ := newRecordedLoginDialog(t, nil)
			pending := dialog.ShowPrompt("Enter the code", "")
			dialog.ShowDeviceCode(device)
			dialog.HandleInput("\r")
			wantLoginActions(t, recorder, 0, 0)
			select {
			case value, ok := <-pending:
				if !ok || value != "" {
					t.Fatalf("the pending input resolved with %q, %v; want the empty submitted value", value, ok)
				}
			default:
				t.Fatal("the confirm key did not reach the pending input's submit")
			}
		})
	})
	t.Run("the device-code confirm needs the dialog focused", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, _ := newRecordedLoginDialog(t, nil)
			dialog.ShowDeviceCode(device)
			dialog.SetFocused(false)
			dialog.HandleInput("\r")
			wantLoginActions(t, recorder, 0, 0)
			dialog.SetFocused(true)
			dialog.HandleInput("\r")
			wantLoginActions(t, recorder, 1, 1)
		})
	})
	t.Run("every cancel press completes and aborts", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, _, completions := newRecordedLoginDialog(t, nil)
			pending := dialog.ShowPrompt("Enter key", "")
			dialog.HandleInput("\x1b")
			dialog.HandleInput("\x03")
			if *completions != 2 || !dialog.Cancelled() {
				t.Fatalf("completions=%d cancelled=%v, want 2 and true", *completions, dialog.Cancelled())
			}
			select {
			case _, ok := <-pending:
				if ok {
					t.Fatal("cancel delivered a value to the pending input")
				}
			default:
				t.Fatal("cancel did not end the pending input")
			}
		})
	})
	t.Run("a device code shown after cancel offers nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, _ := newRecordedLoginDialog(t, nil)
			dialog.HandleInput("\x1b")
			dialog.ShowDeviceCode(device)
			dialog.HandleInput("\r")
			wantLoginActions(t, recorder, 0, 0)
		})
	})
	t.Run("keys with no prompt, URL or device action change nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dialog, recorder, completions := newRecordedLoginDialog(t, nil)
			before := renderedPlain(dialog)
			for _, key := range []string{"a", "\r", "\n", "\x7f", "\x1b[A"} {
				dialog.HandleInput(key)
			}
			wantLoginActions(t, recorder, 0, 0)
			if after := renderedPlain(dialog); after != before || *completions != 0 {
				t.Fatalf("render changed or completed: completions=%d\n%s\n--\n%s", *completions, before, after)
			}
		})
	})
}
