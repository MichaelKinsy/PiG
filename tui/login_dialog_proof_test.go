package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// login-dialog.ts showAuth: a spacer, the URL, and, when instructions are given, another spacer and the instructions in the warning color
// with an indent of one.
func TestLoginDialogShowAuthAddsTheInstructionsAfterASpacer(t *testing.T) {
	plain := func(instructions string) []string {
		dialog := NewLoginDialogComponent(nil, "test", nil, "")
		dialog.ShowAuth("https://auth.example.invalid/x", instructions)
		return stripANSILines(dialog.Render(80))
	}
	without, with := plain(""), plain("Follow the steps")
	if len(with) != len(without)+2 {
		t.Fatalf("instructions added %d rows, want a spacer and the text:\n%q\n%q", len(with)-len(without), without, with)
	}
	// The instructions come last, before the bottom border, after a blank row.
	n := len(with)
	if strings.TrimRight(with[n-3], " ") != "" || strings.TrimRight(with[n-2], " ") != " Follow the steps" {
		t.Fatalf("rows before the border = %q, want a blank row then the indented instructions", with[n-3:n-1])
	}
	dialog := NewLoginDialogComponent(nil, "test", nil, "")
	dialog.ShowAuth("https://auth.example.invalid/x", "Follow the steps")
	if got := strings.Join(dialog.Render(80), "\n"); !strings.Contains(got, ActiveTheme().Fg("warning", "Follow the steps")) {
		t.Fatalf("instructions are not in the warning color:\n%q", got)
	}
}

// login-dialog.ts showDeviceCode sets authUrl to undefined; showAuth sets it: the copy key copies the sign-in URL only while it is the one shown.
func TestLoginDialogCopyKeyCopiesTheShownAuthURLOnly(t *testing.T) {
	authCopyKeybindings(t)
	for name, replace := range map[string]func(*LoginDialogComponent){
		"device code": func(d *LoginDialogComponent) {
			d.ShowDeviceCode(OAuthDeviceCodeInfo{VerificationURI: "https://example.invalid/device", UserCode: "ABCD"})
		},
		"details": func(d *LoginDialogComponent) { d.ShowDetails([]string{"set up first"}) },
	} {
		clipboard := &clipboardRecorder{}
		dialog := NewLoginDialogComponent(nil, "test", nil, "")
		dialog.SetCopyToClipboard(clipboard.copy, nil)
		dialog.ShowAuth("https://auth.example.invalid/x", "")
		dialog.HandleInput("\x18")
		deadline := time.Now().Add(time.Second)
		for len(clipboard.texts()) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got := clipboard.texts(); len(got) != 1 || got[0] != "https://auth.example.invalid/x" {
			t.Fatalf("%s: the shown URL was not copied: %q", name, got)
		}
		replace(dialog)
		dialog.HandleInput("\x18")
		time.Sleep(20 * time.Millisecond)
		if got := clipboard.texts(); len(got) != 1 {
			t.Errorf("%s: the copy key copied a URL that is no longer shown: %q", name, got)
		}
	}
}

// Login diagnostics never show a submitted secret, longer secrets first (a secret that contains another is redacted whole), and the answer channel
// delivers the value once and then closes.
func TestLoginDialogRedactsEverySubmittedSecretLongestFirstAndClosesTheAnswer(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "test", nil, "")
	submit := func(secret string) {
		t.Helper()
		answer := dialog.ShowSecretInput("API key", "")
		dialog.HandleInput("\x1b[200~" + secret + "\x1b[201~")
		dialog.HandleInput("\r")
		if got, ok := <-answer; !ok || got != secret {
			t.Fatalf("answer = %q, %v", got, ok)
		}
		if _, ok := <-answer; ok {
			t.Fatal("the answer channel stayed open after delivering its value")
		}
	}
	submit("abcdefgh")
	submit("abcdefgh1")
	// The longer secret is masked as a whole ("•••••" and its last four characters), not first as the shorter one it contains.
	if got, want := dialog.Redact("key abcdefgh1"), "key •••••fgh1"; got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
	dialog.ShowWaiting("waiting for abcdefgh1")
	if rendered := strings.Join(dialog.Render(120), "\n"); strings.Contains(rendered, "abcdefgh1") {
		t.Errorf("a waiting message shows a submitted secret:\n%s", rendered)
	}
}

// login-dialog.ts:154-160 adds the dialog's one Input to the content container for every prompt, so a prompt shown before an earlier one is answered leaves the same input at both positions; a masked single character counts "1 character".
func TestLoginDialogShowsTheSharedInputAtEveryUnansweredPromptAndCountsOneCharacter(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "test", nil, "")
	dialog.SetFocused(true)
	dialog.ShowPrompt("first", "")
	dialog.ShowPrompt("second", "")
	dialog.HandleInput("x")
	rows := 0
	for _, line := range stripANSILines(dialog.Render(80)) {
		if strings.Contains(line, "> x") {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("%d rows show the typed input, want both unanswered prompts' (one shared Input)", rows)
	}
	masked := NewLoginDialogComponent(nil, "test", nil, "")
	masked.ShowSecretInput("key", "")
	masked.HandleInput("k")
	rendered := widthx.StripAnsi(strings.Join(masked.Render(80), "\n"))
	if !strings.Contains(rendered, "(1 character)") || strings.Contains(rendered, "(1 characters)") {
		t.Errorf("masked preview of one character:\n%s", rendered)
	}
	masked.Success()
	if !masked.Done() {
		t.Error("Success did not mark the dialog done")
	}
}
