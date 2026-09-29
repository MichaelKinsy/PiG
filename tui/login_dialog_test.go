package tui

import (
	"runtime"
	"slices"
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

// Pi's showDeviceCode (login-dialog.ts:118-131): a spacer, the linked verification URL, the click hint, a spacer, then "Enter code: <code>". Pi's notifyAuthDialog follows it with showWaiting.
func TestLoginDialog_ShowDeviceCode(t *testing.T) {
	dlg := NewLoginDialog("GitHub Copilot", nil)
	dlg.ShowDeviceCode("https://github.com/login/device", "WDJB-MJHT")
	dlg.ShowWaiting("Waiting for authentication...")
	lines := dlg.Render(80)
	hint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		hint = "Cmd+click to open"
	}
	want := []string{"", " https://github.com/login/device", " " + hint, "", " Enter code: WDJB-MJHT", "", " Waiting for authentication...", " (escape/ctrl+c to cancel)"}
	body := lines[2 : len(lines)-1]
	if len(body) != len(want) {
		t.Fatalf("device-code lines = %q, want %q", body, want)
	}
	for i, line := range body {
		if got := strings.TrimRight(widthx.StripAnsi(line), " "); got != want[i] {
			t.Errorf("line %d = %q, want %q", i, got, want[i])
		}
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

// Ports packages/coding-agent/test/suite/regressions/5433-extension-oauth-prompt-input.test.ts:40,61,73,87,99.
func TestLoginDialogOAuthPromptsUpstream(t *testing.T) {
	previousTheme, previousKeys := ActiveTheme(), GetTUIKeybindings()
	SetTheme("dark")
	SetTUIKeybindings(NewTUIKeybindingsManager(nil))
	t.Cleanup(func() { storeActiveTheme(previousTheme); SetTUIKeybindings(previousKeys) })
	newDialog := func() *LoginDialog { return NewLoginDialog("Prompt Repro", func() {}) }
	render := func(dialog *LoginDialog) []string {
		lines := strings.Split(widthx.StripAnsi(strings.Join(dialog.Render(120), "\n")), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return lines
	}
	contains := func(t *testing.T, lines []string, values ...string) {
		t.Helper()
		for _, value := range values {
			if !strings.Contains(strings.Join(lines, "\n"), value) {
				t.Errorf("dialog missing %q: %q", value, lines)
			}
		}
	}
	assertValue := func(t *testing.T, result <-chan string, want string) {
		t.Helper()
		select {
		case got, ok := <-result:
			if !ok || got != want {
				t.Fatalf("submitted=%q open=%v want=%q", got, ok, want)
			}
		default:
			t.Fatal("Enter did not complete the prompt")
		}
	}
	for _, tc := range []struct {
		name, prompt, placeholder, value string
		manual                           bool
	}{
		{"keeps previous prompt input stable when a later prompt is active", "First prompt:", "first-value", "first-value", false},
		{"keeps previous manual input stable when a later prompt is active", "Paste callback URL:", "", "callback-value", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialog := newDialog()
			var first <-chan string
			if tc.manual {
				first = dialog.ShowManualInput(tc.prompt)
			} else {
				first = dialog.ShowInput(tc.prompt, tc.placeholder)
			}
			dialog.HandleInput(tc.value)
			dialog.HandleInput("\n")
			assertValue(t, first, tc.value)
			second := dialog.ShowInput("Second prompt:", "")
			dialog.HandleInput("second-secret-demo")
			lines := render(dialog)
			contains(t, lines, tc.prompt, "Second prompt:")
			for _, value := range []string{tc.value, "second-secret-demo"} {
				count := 0
				for _, line := range lines {
					if strings.TrimSpace(line) == "> "+value {
						count++
					}
				}
				if count != 1 {
					t.Errorf("rendered %q %d times, want once: %q", value, count, lines)
				}
			}
			dialog.HandleInput("\n")
			assertValue(t, second, "second-secret-demo")
		})
	}
	t.Run("preserves auth instructions when showing a prompt", func(t *testing.T) {
		dialog := newDialog()
		dialog.ShowAuth("https://example.invalid/login", "Authorize the extension")
		dialog.ShowInput("First prompt:", "")
		contains(t, render(dialog), "https://example.invalid/login", "Authorize the extension", "First prompt:")
	})
	t.Run("preserves neutral information and links when showing a prompt", func(t *testing.T) {
		dialog := newDialog()
		dialog.ShowInfo("Configure credentials outside pi.", []AuthInfoLink{{Label: "Provider documentation", URL: "https://example.invalid/docs"}}, false)
		dialog.ShowInput("Press Enter to continue:", "")
		contains(t, render(dialog), "Configure credentials outside pi.", "Provider documentation: https://example.invalid/docs", "Press Enter to continue:")
	})
	t.Run("preserves setup details when showing a prompt", func(t *testing.T) {
		dialog := newDialog()
		dialog.ShowDetails([]string{"AWS credential setup:", "providers.md"})
		dialog.ShowInput("Enter API key:", "")
		contains(t, render(dialog), "AWS credential setup:", "providers.md", "Enter API key:")
	})
}

// packages/coding-agent/src/modes/interactive/components/login-dialog.ts:137-143: manual input uses a dim label and a cancel-only hint, unlike showPrompt.
func TestLoginDialogManualInputLayout(t *testing.T) {
	previous := GetTUIKeybindings()
	SetTUIKeybindings(NewTUIKeybindingsManager(nil))
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	dialog := NewLoginDialog("Prompt Repro", nil)
	answer := dialog.ShowManualInput("Paste callback URL:")
	dialog.HandleInput("callback-value")
	dialog.HandleInput("\n")
	if got := <-answer; got != "callback-value" {
		t.Fatalf("submission=%q", got)
	}
	lines := dialog.Render(120)
	got := make([]string, 0, len(lines)-3)
	for _, line := range lines[2 : len(lines)-1] {
		got = append(got, strings.TrimRight(widthx.StripAnsi(line), " "))
	}
	want := []string{"", " Paste callback URL:", "> callback-value", " (escape/ctrl+c to cancel)"}
	if !slices.Equal(got, want) {
		t.Fatalf("manual input body=%q want=%q", got, want)
	}
	wantLabel := NewPaddedText(ActiveTheme().FgText("dim", "Paste callback URL:"), 1, 0, nil).Render(120)
	if !slices.Equal(lines[3:4], wantLabel) {
		t.Fatalf("manual input label=%q want=%q", lines[3:4], wantLabel)
	}
}
