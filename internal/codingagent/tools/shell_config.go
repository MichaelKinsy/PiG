// Shell config resolver for the bash tool and user bash.
//
// Mirrors upstream packages/coding-agent/src/utils/shell.ts::getShellConfig.
// Resolution order:
//
//  1. settings.shellPath (must exist, else "Custom shell path not found")
//  2. platform default (defaultShellConfig):
//       unix:    /bin/bash, then bash on PATH, then sh
//       windows: Git Bash (%ProgramFiles%\Git\bin\bash.exe, then x86), then
//                bash.exe on PATH, else a helpful "install Git Bash" error
//
// Upstream never consults $SHELL: the tool runs bash syntax, so the user's
// login shell (zsh, fish) is not a substitute.

package tools

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// ShellConfig is the resolved (binary, leading-args) pair used to invoke
// shell commands. The actual command string is appended as the final
// argument when spawning, unless CommandTransport is "stdin".
type ShellConfig = shellconfig.Config

// SettingsView is the minimum settings surface needed to resolve a
// ShellConfig. Defining it here (rather than importing internal/codingagent)
// keeps the tools package free of upward dependencies.
type SettingsView interface {
	GetShellPath() (string, error)
}

// GetShellConfig returns the shell binary + leading args. settings may be
// nil; resolution then uses the platform default (defaultShellConfig).
func GetShellConfig(settings SettingsView) (ShellConfig, error) {
	custom := ""
	if settings != nil {
		var err error
		if custom, err = settings.GetShellPath(); err != nil {
			return ShellConfig{}, err
		}
	}
	return getShellConfig(custom)
}

// getShellConfig mirrors upstream getShellConfig(customShellPath).
func getShellConfig(customShellPath string) (ShellConfig, error) {
	if customShellPath != "" {
		if _, err := os.Stat(customShellPath); err == nil {
			return shellconfig.ForBash(customShellPath), nil
		}
		return ShellConfig{}, fmt.Errorf("Custom shell path not found: %s", customShellPath)
	}
	return defaultShellConfig()
}

// defaultShellConfig is upstream getShellConfig's platform default.
func defaultShellConfig() (ShellConfig, error) { return shellconfig.Default() }
