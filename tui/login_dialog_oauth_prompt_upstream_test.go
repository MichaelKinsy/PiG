package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestLoginDialogPreviousPromptInputStableUpstream(t *testing.T) {
	for _, tc := range []struct{ name, prompt, value, placeholder string }{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5433-extension-oauth-prompt-input.test.ts:40
		{"keeps previous prompt input stable when a later prompt is active", "First prompt:", "first-value", "first-value"},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5433-extension-oauth-prompt-input.test.ts:99
		{"keeps previous manual input stable when a later prompt is active", "Paste callback URL:", "callback-value", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialog := NewLoginDialogComponent(nil, "Prompt Repro", nil, "")
			first := dialog.ShowPrompt(tc.prompt, tc.placeholder)
			dialog.HandleInput(tc.value)
			dialog.HandleInput("\n")
			if got := <-first; got != tc.value {
				t.Fatalf("submitted=%q", got)
			}
			second := dialog.ShowPrompt("Second prompt:", "")
			dialog.HandleInput("second-secret-demo")
			lines := loginDialogPlainLines(dialog)
			text := strings.Join(lines, "\n")
			if !strings.Contains(text, tc.prompt) || !strings.Contains(text, "Second prompt:") {
				t.Errorf("prompts missing:\n%s", text)
			}
			for _, value := range []string{tc.value, "second-secret-demo"} {
				count := 0
				for _, line := range lines {
					if strings.TrimSpace(line) == "> "+value {
						count++
					}
				}
				if count != 1 {
					t.Errorf("%q rendered %d times:\n%s", value, count, text)
				}
			}
			dialog.HandleInput("\n")
			if got := <-second; got != "second-secret-demo" {
				t.Fatalf("submitted=%q", got)
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5433-extension-oauth-prompt-input.test.ts:61
func TestLoginDialogPreservesAuthInstructionsWithPromptUpstream(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "Prompt Repro", nil, "")
	dialog.ShowAuth("https://example.invalid/login", "Authorize the extension")
	dialog.ShowPrompt("First prompt:", "")
	requireLoginDialogText(t, dialog, "https://example.invalid/login", "Authorize the extension", "First prompt:")
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5433-extension-oauth-prompt-input.test.ts:73
func TestLoginDialogPreservesNeutralInformationAndLinksUpstream(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "Prompt Repro", nil, "")
	dialog.ShowInfo("Configure credentials outside pi.", []AuthInfoLink{{Label: "Provider documentation", URL: "https://example.invalid/docs"}}, false)
	dialog.ShowPrompt("Press Enter to continue:", "")
	requireLoginDialogText(t, dialog, "Configure credentials outside pi.", "Provider documentation: https://example.invalid/docs", "Press Enter to continue:")
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5433-extension-oauth-prompt-input.test.ts:87
func TestLoginDialogPreservesSetupDetailsUpstream(t *testing.T) {
	dialog := NewLoginDialogComponent(nil, "Prompt Repro", nil, "")
	dialog.ShowDetails([]string{"AWS credential setup:", "providers.md"})
	dialog.ShowPrompt("Enter API key:", "")
	requireLoginDialogText(t, dialog, "AWS credential setup:", "providers.md", "Enter API key:")
}

func loginDialogPlainLines(dialog *LoginDialogComponent) []string {
	lines := dialog.Render(120)
	for i, line := range lines {
		lines[i] = strings.TrimRight(widthx.StripAnsi(line), " ")
	}
	return lines
}
func requireLoginDialogText(t *testing.T, dialog *LoginDialogComponent, values ...string) {
	t.Helper()
	text := strings.Join(loginDialogPlainLines(dialog), "\n")
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Errorf("missing %q:\n%s", value, text)
		}
	}
}
