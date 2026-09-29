package main

import "github.com/MichaelKinsy/PiG/internal/codingagent"

// startupUIOptions returns the options for prompts shown before Session selection: the Session picker, the missing-cwd prompt, and the project trust prompt.
// They read manager's merged settings, and an interactive --use-theme is applied to manager first, as Pi's startup settings manager does (main.ts:656,666-667).
func startupUIOptions(flags CLIFlags, interactive bool, cwd, agentDir string, manager *codingagent.SettingsManager) codingagent.StartupUIOptions {
	if interactive && flags.UseTheme != nil {
		manager.ApplyOverrides(codingagent.ThemeOverride(*flags.UseTheme))
	}
	return codingagent.StartupUIOptions{
		AgentDir:   agentDir,
		Settings:   manager.Get(),
		ThemePaths: collectStartupThemePaths(cwd, agentDir, manager),
	}
}
