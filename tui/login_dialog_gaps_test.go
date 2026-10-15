package tui

import (
	"io"
	"strings"
	"testing"
	"time"
)

func loginDialogText(d *LoginDialogComponent) string {
	return stripANSI(strings.Join(d.Render(80), "\n"))
}

func receiveWithin(t *testing.T, ch <-chan string) (string, bool) {
	t.Helper()
	select {
	case v, ok := <-ch:
		return v, ok
	case <-time.After(5 * time.Second):
		t.Fatal("no value or close on the prompt channel")
		return "", false
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/login-dialog.ts constructor, showPrompt, showProgress, showWaiting, cancel.
// The dialog is titled "Login to <provider>"; a prompt shows its text and placeholder, delivers the submitted value on its channel and replaces the input row with "> <value>"; progress and waiting lines are appended; a submit with nothing pending changes nothing.
func TestLoginDialogComponentPromptFlow(t *testing.T) {
	var completed []string
	d := NewLoginDialogComponent(NewWithOutput(io.Discard, 80, 24), "anthropic", func(ok bool, msg string) { completed = append(completed, msg) }, "")
	if out := loginDialogText(d); !strings.Contains(out, "Login to anthropic") {
		t.Fatalf("title missing:\n%s", out)
	}
	named := NewLoginDialogComponent(nil, "anthropic", nil, "Claude Pro")
	if out := loginDialogText(named); !strings.Contains(out, "Login to Claude Pro") || strings.Contains(out, "anthropic") {
		t.Fatalf("provider name override ignored:\n%s", out)
	}
	if out := loginDialogText(NewLoginDialogComponent(nil, "x", nil, "", "Custom title")); !strings.Contains(out, "Custom title") || strings.Contains(out, "Login to") {
		t.Fatalf("title override ignored:\n%s", out)
	}

	d.ShowProgress("step one")
	ch := d.ShowPrompt("Enter your token", "sk-123")
	out := loginDialogText(d)
	for _, want := range []string{"step one", "Enter your token", "e.g., sk-123", "to cancel", "to submit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt view lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "step one") > strings.Index(out, "Enter your token") {
		t.Fatalf("progress appears after the prompt:\n%s", out)
	}
	if strings.Contains(out, secretInputHint) {
		t.Fatalf("an ordinary prompt shows the secret hint:\n%s", out)
	}
	for _, r := range "hello" {
		d.HandleInput(string(r))
	}
	if !strings.Contains(loginDialogText(d), "hello") {
		t.Fatalf("typed text not shown:\n%s", loginDialogText(d))
	}
	d.HandleInput("\r")
	if got, ok := receiveWithin(t, ch); !ok || got != "hello" {
		t.Fatalf("submitted value = %q (open %v)", got, ok)
	}
	out = loginDialogText(d)
	if !strings.Contains(out, "> hello") || !strings.Contains(out, "to submit") {
		t.Fatalf("the submitted text did not replace the input:\n%s", out)
	}
	if _, ok := receiveWithin(t, ch); ok {
		t.Fatal("the prompt channel stayed open after the submit")
	}
	if d.input != nil || d.inputActive {
		t.Fatal("the submitted prompt's input stayed active")
	}
	d.HandleInput("x")
	if strings.Contains(loginDialogText(d), "hellox") {
		t.Fatal("input reached a finished prompt")
	}

	d.ShowWaiting("Waiting for browser")
	if lines := strings.Split(loginDialogText(d), "\n"); !func() bool {
		for i, l := range lines {
			if strings.Contains(l, "Waiting for browser") {
				return i > 0 && strings.TrimSpace(lines[i-1]) == ""
			}
		}
		return false
	}() {
		t.Fatalf("a waiting message follows a spacer:\n%s", loginDialogText(d))
	}
	if out := loginDialogText(d); !strings.Contains(out, "Waiting for browser") || strings.Count(out, "to cancel") < 2 {
		t.Fatalf("waiting view:\n%s", out)
	}
	if d.Done() || d.Cancelled() || len(completed) != 0 {
		t.Fatalf("dialog finished early: done %v cancelled %v %v", d.Done(), d.Cancelled(), completed)
	}
	d.Success()
	if !d.Done() || d.Cancelled() {
		t.Fatalf("Success: done %v cancelled %v", d.Done(), d.Cancelled())
	}
}

// login-dialog.ts: cancel reports "Login cancelled" once, marks the dialog done and cancelled and closes the pending input; the focus flag reaches the input of the active prompt.
func TestLoginDialogComponentCancelAndFocus(t *testing.T) {
	var msgs []string
	var oks []bool
	d := NewLoginDialogComponent(nil, "p", func(ok bool, msg string) { oks = append(oks, ok); msgs = append(msgs, msg) }, "")
	if d.Focused() {
		t.Fatal("a new dialog is focused")
	}
	d.SetFocused(true)
	ch := d.ShowPrompt("Code", "")
	if !d.Focused() || !d.input.Focused {
		t.Fatalf("focus not applied to a prompt shown afterwards: dialog %v input %v", d.Focused(), d.input.Focused)
	}
	d.SetFocused(false)
	if d.Focused() || d.input.Focused {
		t.Fatal("focus not removed from the active input")
	}
	d.HandleInput("\x1b")
	if _, ok := receiveWithin(t, ch); ok {
		t.Fatal("cancel did not close the pending input")
	}
	d.input.OnSubmit("late")
	if strings.Contains(loginDialogText(d), "> late") || len(d.secrets) != 0 {
		t.Fatalf("a submit after the cancel changed the dialog:\n%s", loginDialogText(d))
	}
	if !d.Done() || !d.Cancelled() || len(msgs) != 1 || msgs[0] != "Login cancelled" || oks[0] {
		t.Fatalf("cancel: done %v cancelled %v msgs %v oks %v", d.Done(), d.Cancelled(), msgs, oks)
	}
}

// login-dialog.ts + D80: with masking on (the default) a secret prompt shows the hint, history keeps only a bounded preview, and the typed value is redacted from later diagnostics, including while it is still being typed; a plain prompt and SetMaskSecretInput(false) keep Pi's literal text.
func TestLoginDialogComponentSecretMaskingAndRedaction(t *testing.T) {
	const secret = "sk-very-secret-token"
	d := NewLoginDialogComponent(nil, "p", nil, "")
	ch := d.ShowSecretInput("API key", "")
	if out := loginDialogText(d); !strings.Contains(out, secretInputHint) {
		t.Fatalf("masked prompt lacks the hint:\n%s", out)
	}
	for _, r := range secret {
		d.HandleInput(string(r))
	}
	if got := d.Redact("bad " + secret); strings.Contains(got, secret) || !strings.HasPrefix(got, "bad ") {
		t.Fatalf("typed secret not redacted: %q", got)
	}
	if out := loginDialogText(d); strings.Contains(out, secret) {
		t.Fatalf("the secret is displayed while typing:\n%s", out)
	}
	d.HandleInput("\r")
	if got, _ := receiveWithin(t, ch); got != secret {
		t.Fatalf("the channel carries %q, want the original value", got)
	}
	out := loginDialogText(d)
	preview, _ := SecretInputPreview(secret)
	if strings.Contains(out, secret) || !strings.Contains(out, "> "+preview) {
		t.Fatalf("history after submit:\n%s\nwant preview %q", out, preview)
	}
	if got := d.Redact("failed with " + secret); strings.Contains(got, secret) {
		t.Fatalf("a submitted secret is not redacted: %q", got)
	}
	d.ShowProgress("token " + secret)
	if out := loginDialogText(d); strings.Contains(out, secret) {
		t.Fatalf("progress leaks the secret:\n%s", out)
	}

	plain := NewLoginDialogComponent(nil, "p", nil, "")
	plain.SetMaskSecretInput(false)
	pc := plain.ShowSecretInput("API key", "")
	for _, r := range "abcdefgh" {
		plain.HandleInput(string(r))
	}
	plain.HandleInput("\r")
	receiveWithin(t, pc)
	if out := loginDialogText(plain); !strings.Contains(out, "> abcdefgh") || strings.Contains(out, secretInputHint) {
		t.Fatalf("unmasked secret prompt:\n%s", out)
	}
	if got := plain.Redact("abcdefgh"); got != "abcdefgh" {
		t.Fatalf("an unmasked value was redacted: %q", got)
	}

	manual := NewLoginDialogComponent(nil, "p", nil, "")
	manual.ShowManualInput("Paste the code")
	if !strings.Contains(strings.Join(manual.Render(80), "\n"), ActiveTheme().Fg("dim", "Paste the code")) {
		t.Fatal("a manual prompt is not drawn in the dim color")
	}
	if out := loginDialogText(manual); !strings.Contains(out, "Paste the code") || strings.Contains(out, "to submit") {
		t.Fatalf("manual input hint:\n%s", out)
	}
}

// login-dialog.ts showAuth / showDeviceCode / showInfo: showAuth and showDeviceCode replace the earlier content, showInfo appends; the authorization URL is shown with the instructions after it, the device code under its verification URL, and info shows its message, labelled links and an optional close hint.
func TestLoginDialogComponentShowsAuthDeviceCodeDetailsAndInfo(t *testing.T) {
	d := NewLoginDialogComponent(nil, "p", nil, "")
	d.ShowProgress("old line")
	d.ShowAuth("https://example.test/authorize", "Open it in a browser")
	out := loginDialogText(d)
	if strings.Contains(out, "old line") {
		t.Fatalf("ShowAuth kept the earlier content:\n%s", out)
	}
	if u, i := strings.Index(out, "https://example.test/authorize"), strings.Index(out, "Open it in a browser"); u < 0 || i < u {
		t.Fatalf("URL/instructions order:\n%s", out)
	}
	d.ShowAuth("https://example.test/second", "")
	if out := loginDialogText(d); strings.Contains(out, "authorize") || strings.Contains(out, "Open it") || !strings.Contains(out, "second") {
		t.Fatalf("a second ShowAuth did not replace the first:\n%s", out)
	}

	d.ShowDeviceCode(OAuthDeviceCodeInfo{UserCode: "ABCD-1234", VerificationURI: "https://example.test/device"})
	out = loginDialogText(d)
	if strings.Contains(out, "second") || !strings.Contains(out, "Enter code: ABCD-1234") || strings.Index(out, "device") > strings.Index(out, "Enter code") {
		t.Fatalf("device code view:\n%s", out)
	}
	if d.authURL != nil {
		t.Fatal("the device-code view kept an auth URL to copy")
	}

	d.ShowAuth("https://example.test/third", "")
	d.ShowDetails([]string{"first detail", "second detail"})
	if d.authURL != nil {
		t.Fatal("the details view kept an auth URL to copy")
	}
	if !strings.Contains(strings.Join(d.Render(80), "\n"), " first detail") || strings.Contains(strings.Join(strings.Fields(loginDialogText(d)), ""), "third") {
		t.Fatalf("details view:\n%s", loginDialogText(d))
	}
	out = loginDialogText(d)
	if strings.Contains(out, "ABCD-1234") || !strings.Contains(out, "first detail") || strings.Index(out, "first detail") > strings.Index(out, "second detail") {
		t.Fatalf("details view:\n%s", out)
	}
	d.ShowInfo("Read the docs", []AuthInfoLink{{URL: "https://docs.test/a", Label: "Guide"}, {URL: "https://docs.test/b"}}, false)
	out = loginDialogText(d)
	for _, want := range []string{"first detail", "Read the docs", "Guide: https://docs.test/a", "https://docs.test/b"} {
		if !strings.Contains(out, want) {
			t.Fatalf("info view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "to close") || strings.Contains(out, "b: https") {
		t.Fatalf("info view:\n%s", out)
	}
	d.ShowInfo("Bye", nil, true)
	if out := loginDialogText(d); !strings.Contains(out, "to close") {
		t.Fatalf("close hint missing:\n%s", out)
	}
	if !strings.Contains(strings.Join(d.Render(80), "\n"), "\x1b]8;;https://docs.test/a\x07") {
		t.Fatal("the info link is not a terminal hyperlink")
	}
}
