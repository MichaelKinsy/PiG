package cli

import (
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/configroot"
)

// llamaBuiltinName is the name of the built-in llama.cpp extension, the first entry of builtInExtensions.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:8.
const llamaBuiltinName = "llama.cpp"

// nativeBuiltInExtensions are the built-in extensions of the CLI, in extensions/index.ts order: llama.cpp (whose provider the llama
// host supplies), codemode (its sandbox runs the model's JavaScript in QuickJS on wazero), tool-search and mcp. settings supplies
// `codemode.mode`, `codemode.inlineBudget` and the agent directory of `mcp.json` on every use; nil means the defaults.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14.
func nativeBuiltInExtensions(settings *codingagent.SettingsManager) []extension.InlineExtension {
	var options builtin.Options
	options.ConfigureCodemode(wazeroCacheDir())
	var agentDir string
	if settings != nil {
		agentDir = settings.AgentDir()
	} else if dir, err := codingagent.ResolveAgentDir(); err == nil {
		// The package-initialization list (builtInExtensions) is built before main can report an unresolvable home directory; it is the name list, and the session's list is built from the session's settings.
		agentDir = dir
	}
	options.ConfigureMcp(agentDir, codingagent.ConfigDirName(), codingagent.OpenBrowser, codingagent.CopyToClipboard)
	// pig additive (D92): a Binary without llama.cpp has no llama.cpp entry.
	inline := llamaInlineExtensions()
	for _, entry := range builtin.All(options) {
		inline = append(inline, extension.NamedInlineExtension{Name: entry.Name, Factory: entry.Factory, Replaceable: entry.Replaceable, Builtin: true})
	}
	return inline
}

// wazeroCacheDir is where the codemode sandbox keeps wazero's compilation cache: `cache/wazero` under PIG_HOME, else
// under the user cache directory. It is an optimization only; an unusable directory is ignored.
func wazeroCacheDir() string {
	if os.Getenv("PIG_HOME") != "" {
		if root, err := configroot.Resolve(); err == nil {
			return filepath.Join(root, "cache", "wazero")
		}
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "pig", "wazero")
	}
	return ""
}
