package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.99.2 cli/startup-ui.ts showStartupSelector and showStartupInput `await createStartupTui(settingsManager)`
// (:84-92) before they construct the component: createStartupTui registers the themes, marks the terminal colors
// pending and runs initTheme, so ExtensionSelectorComponent (extension-selector.ts:48-84) and
// ExtensionInputComponent build their themed title and rows under the grayscale system theme. The probe in tmux
// (parity 10-startup-trust-prompt-wording) draws the heading and the selected row without a foreground color.
func TestStartupPromptComponentsAreBuiltUnderThePendingSystemTheme(t *testing.T) {
	for name, build := range map[string]func(StartupUIOptions) tui.Component{
		"selector": func(opts StartupUIOptions) tui.Component {
			return newStartupSelector("Trust project folder?", []string{"Trust", "Do not trust"}, opts)
		},
		"input": func(opts StartupUIOptions) tui.Component {
			return newStartupInput("Name", "placeholder", opts)
		},
	} {
		t.Run(name, func(t *testing.T) {
			restoreStartupTheme(t)
			// The terminal colors of an earlier prompt ended the pending state; a new prompt starts a new query.
			tui.SetTerminalColors(tui.TerminalColors{})
			tui.SetThemeByName(tui.SystemThemeName)
			component := build(StartupUIOptions{})
			for _, line := range component.Render(80) {
				if strings.Contains(line, "\x1b[38;") {
					t.Fatalf("a prompt built before the terminal reports its colors draws a foreground color: %q", line)
				}
			}
		})
	}
}

// Pi 0.99.2 createStartupTui applies the terminal capability overrides (cli/startup-ui.ts:85) before initTheme
// (:89) creates the theme in getCapabilities().trueColor's color mode, and the prompt components bake their themed
// rows at construction. A `terminal.trueColor: false` setting therefore draws the rows in 256 colors even where
// COLORTERM advertises truecolor, and keeps them after the terminal reports its colors.
func TestStartupPromptComponentsAreBuiltUnderTheCapabilityOverrides(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
	for _, env := range []string{"TERM_PROGRAM", "TMUX", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "ITERM_SESSION_ID", "WT_SESSION"} {
		t.Setenv(env, "")
	}
	restoreStartupTheme(t)
	t.Cleanup(func() {
		tui.SetCapabilityOverrides(tui.CapabilityOverrides{})
		tui.ResetCapabilitiesCache()
		tui.RefreshActiveThemeColorMode()
	})
	tui.SetCapabilityOverrides(tui.CapabilityOverrides{})
	tui.ResetCapabilitiesCache()
	if !tui.GetCapabilities().TrueColor {
		t.Fatal("COLORTERM=truecolor did not advertise truecolor")
	}
	var settings Settings
	if err := json.Unmarshal([]byte(`{"theme":"dark","terminal":{"trueColor":false}}`), &settings); err != nil {
		t.Fatal(err)
	}
	selector := newStartupSelector("Trust project folder?", []string{"Trust", "Do not trust"}, StartupUIOptions{Settings: settings})
	rendered := strings.Join(selector.Render(80), "\n")
	if strings.Contains(rendered, "\x1b[38;2;") || !strings.Contains(rendered, "\x1b[38;5;") {
		t.Fatalf("a prompt built with terminal.trueColor=false drew truecolor rows: %q", rendered)
	}
}
