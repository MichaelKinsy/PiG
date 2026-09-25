package tui

import (
	"runtime"
	"strings"
	"testing"
)

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
	if !strings.Contains(joined, "Esc to cancel") {
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

func TestLoginDialog_CancelClosesPendingInputChannel(t *testing.T) {
	dlg := NewLoginDialog("Test", nil)
	ch := dlg.ShowInput("Paste URL:", "")
	dlg.HandleInput("\x1b")
	if _, ok := <-ch; ok {
		t.Fatal("expected input channel to be closed on cancel")
	}
}
