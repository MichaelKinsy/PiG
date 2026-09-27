package tui

import (
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestLoginDialogSecretValueNeverRendered(t *testing.T) {
	for _, value := range []string{"sk-secret-never-render", "", "key-密碼-🔑"} {
		dlg := NewLoginDialog("Test", nil)
		answer := dlg.ShowSecretInput("API key", "")
		dlg.HandleInput("\x1b[200~" + value + "\x1b[201~")
		check := func(phase string) {
			if got := strings.Join(dlg.Render(120), "\n"); value != "" && strings.Contains(got, value) {
				t.Errorf("secret appears during %s", phase)
			}
			if value != "" && strings.Contains(strings.Join(dlg.lines, "\n"), value) {
				t.Errorf("secret retained in dialog lines during %s", phase)
			}
		}
		check("editing")
		dlg.HandleInput("\r")
		if got := <-answer; got != value {
			t.Fatalf("submitted secret was changed")
		}
		check("submission")
		dlg.ShowProgress("Checking credentials...")
		check("progress")
		dlg.ShowInput("Next non-secret prompt", "")
		check("next prompt")
	}
}

func TestLoginDialogMaskedPreview(t *testing.T) {
	for _, tc := range []struct{ value, preview, count string }{
		{"", "", "0 characters"},
		{"abcd", "••••", "4 characters"},
		{"abcdefgh", "••••efgh", "8 characters"},
		{"prefix-long-abcd", "••••••••abcd", "16 characters"},
		{"x😀界e\u0301Z", "•😀界e\u0301Z", "5 characters"},
	} {
		t.Run(tc.count, func(t *testing.T) {
			d := NewLoginDialog("Test", nil)
			answer := d.ShowSecretInput("API key", "")
			d.HandleInput(tc.value)
			got := strings.Join(d.Render(150), "\n")
			for _, want := range []string{tc.preview, tc.count, "Input hidden (PiG default). Show like Pi: /settings → Mask secret input"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in masked editing frame", want)
				}
			}
			d.HandleInput("\r")
			if value := <-answer; value != tc.value {
				t.Fatal("mask changed submitted value")
			}
			d.ShowProgress("Server echoed " + tc.value)
			got = strings.Join(d.Render(150), "\n")
			if !strings.Contains(got, "> "+tc.preview) {
				t.Error("submitted preview missing")
			}
			if tc.value != "" && strings.Contains(got, tc.value) {
				t.Error("full secret leaked through dialog content")
			}
		})
	}
}

func TestLoginDialog_Render(t *testing.T) {
	dlg := NewLoginDialog("GitHub Copilot", nil)
	lines := dlg.Render(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Login to GitHub Copilot") {
		t.Errorf("missing title in render:\n%s", joined)
	}
}

func TestLoginDialog_ShowAuth(t *testing.T) {
	dlg := NewLoginDialog("GitHub Copilot", nil)
	dlg.ShowAuth("https://github.com/login/device", "")
	lines := dlg.Render(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "https://github.com/login/device") {
		t.Errorf("missing URL:\n%s", joined)
	}
	hint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		hint = "Cmd+click to open"
	}
	if !strings.Contains(joined, hint) {
		t.Errorf("missing hint:\n%s", joined)
	}
}

func TestLoginDialog_ShowWaiting(t *testing.T) {
	dlg := NewLoginDialog("Anthropic", nil)
	dlg.ShowAuth("https://example.com", "")
	dlg.ShowWaiting("Waiting for authorization...")
	lines := dlg.Render(80)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "Waiting for authorization...") {
		t.Errorf("missing waiting message:\n%s", joined)
	}
	if !strings.Contains(widthx.StripAnsi(joined), "escape/ctrl+c to cancel") {
		t.Errorf("missing cancel hint:\n%s", joined)
	}
}

func TestLoginDialog_Cancel(t *testing.T) {
	var cancelCalled bool
	dlg := NewLoginDialog("Test", func() { cancelCalled = true })
	dlg.HandleInput("\x1b")

	if !dlg.Done() {
		t.Fatal("expected Done after Esc")
	}
	if !dlg.Cancelled() {
		t.Fatal("expected Cancelled")
	}
	if !cancelCalled {
		t.Fatal("onCancel not called")
	}
}

func TestLoginDialog_InputSubmit(t *testing.T) {
	dlg := NewLoginDialog("Test", nil)
	ch := dlg.ShowInput("Paste URL:", "")

	dlg.HandleInput("h")
	dlg.HandleInput("i")
	dlg.HandleInput("\n")

	val := <-ch
	if val != "hi" {
		t.Errorf("expected 'hi', got %q", val)
	}
}

func TestLoginDialog_SubmittedInputRemainsVisible(t *testing.T) {
	// Pi login-dialog.ts:59-61,77-81 replaces only the input with Text("> <value>").
	dlg := NewLoginDialog("Test", nil)
	ch := dlg.ShowInput("Paste URL:", "redirect URL")
	dlg.HandleInput("https://example.test/callback?code=hello")
	dlg.HandleInput("\r")
	<-ch
	dlg.ShowProgress("Exchanging code...")
	got := widthx.StripAnsi(strings.Join(dlg.Render(120), "\n"))
	for _, want := range []string{"Paste URL:", "e.g., redirect URL", "> https://example.test/callback?code=hello", "to submit)", "Exchanging code..."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q after submit:\n%s", want, got)
		}
	}
	if strings.Index(got, "> https:") > strings.Index(got, "Exchanging code...") {
		t.Fatal("submitted text moved after progress")
	}
}

func TestLoginDialog_BracketedPasteReachesInput(t *testing.T) {
	dlg := NewLoginDialog("Test", nil)
	ch := dlg.ShowInput("Paste URL:", "")
	const value = "https://example.test/callback?code=pasted&state=ok"
	dlg.HandleInput("\x1b[200~" + value + "\x1b[201~")
	select {
	case <-ch:
		t.Fatal("paste submitted before Enter")
	default:
	}
	dlg.HandleInput("\r")
	if got := <-ch; got != value {
		t.Fatalf("paste = %q, want %q", got, value)
	}
}

func TestLoginDialog_CancelClosesPendingInputChannel(t *testing.T) {
	dlg := NewLoginDialog("Test", nil)
	ch := dlg.ShowInput("Paste URL:", "")
	dlg.HandleInput("\x1b")
	if _, ok := <-ch; ok {
		t.Fatal("expected input channel to be closed on cancel")
	}
}
