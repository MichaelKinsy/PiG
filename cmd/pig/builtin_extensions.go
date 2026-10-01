package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// nativeBuiltInExtensions are the built-in extensions of the CLI, in extensions/index.ts order: llama.cpp (whose provider the llama
// host supplies), codemode (its sandbox runs the model's JavaScript in QuickJS on wazero), tool-search and mcp. settings supplies
// `codemode.mode`, `codemode.inlineBudget` and the agent directory of `mcp.json` on every use; nil means the defaults.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14.
func nativeBuiltInExtensions(settings *codingagent.SettingsManager) []inlineExtension {
	var options builtin.Options
	var getSettings func() extension.Settings
	if settings != nil {
		getSettings = func() extension.Settings { return codemodeSettings(settings) }
	}
	options.ConfigureCodemode(wazeroCacheDir(), getSettings)
	agentDir := codingagent.AgentDir()
	if settings != nil {
		agentDir = settings.AgentDir()
	}
	options.ConfigureMcp(agentDir, codingagent.ConfigDirName(), codingagent.OpenBrowser)
	inline := []inlineExtension{{Name: llamaBuiltinName, Factory: llamaExtension, Builtin: true}}
	for _, entry := range builtin.All(options) {
		inline = append(inline, inlineExtension{Name: entry.Name, Factory: entry.Factory, Replaceable: entry.Replaceable, Builtin: true})
	}
	return inline
}

// codemodeSettings is the `codemode` object of the effective settings, as extension.Settings carries it.
func codemodeSettings(settings *codingagent.SettingsManager) extension.Settings {
	configured := settings.GetSettings().Codemode
	if configured == nil {
		return extension.Settings{}
	}
	object := map[string]any{}
	if configured.Mode != "" {
		object["mode"] = string(configured.Mode)
	}
	if configured.InlineBudget != nil {
		object["inlineBudget"] = float64(*configured.InlineBudget)
	}
	return extension.Settings{"codemode": object}
}

// wazeroCacheDir is where the codemode sandbox keeps wazero's compilation cache: `cache/wazero` under PIG_HOME, else
// under the user cache directory. It is an optimization only; an unusable directory is ignored.
func wazeroCacheDir() string {
	if root := strings.TrimSpace(os.Getenv("PIG_HOME")); root != "" {
		return filepath.Join(root, "cache", "wazero")
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "pig", "wazero")
	}
	return ""
}
