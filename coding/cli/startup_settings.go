package cli

import (
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// startupUIOptions returns the options for prompts shown before Session selection: the Session picker, the missing-cwd prompt, and the project trust prompt.
// They read manager's merged settings, and an interactive --use-theme is applied to manager first, as Pi's startup settings manager does (main.ts:656,666-667).
func startupUIOptions(flags Args, interactive bool, cwd, agentDir string, manager *codingagent.SettingsManager) codingagent.StartupUIOptions {
	if interactive && flags.UseTheme != nil {
		manager.ApplyOverrides(codingagent.ThemeOverride(*flags.UseTheme))
	}
	options := codingagent.StartupUIOptions{
		AgentDir: agentDir,
		Settings: manager.Get(),
	}
	// pig additive (D92): a Piglet that strips themes loads no custom theme, also before Session selection.
	if !pigstrip.Has(pigstrip.ListFeatures, pigstrip.Themes) {
		options.ThemePaths = collectStartupThemePaths(cwd, agentDir, manager)
	}
	return options
}
