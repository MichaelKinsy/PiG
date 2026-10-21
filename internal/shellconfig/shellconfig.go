// Package shellconfig resolves the bash shell PiG runs commands through,
// mirroring upstream packages/coding-agent/src/utils/shell.ts getShellConfig
// and getBashShellConfig. Upstream never consults $SHELL: commands use bash
// syntax, so a login shell such as zsh or fish is not a substitute.
package shellconfig

import (
	"os"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// Config is the resolved shell binary and its leading arguments. The command
// string is appended as the final argument when spawning, unless
// CommandTransport is "stdin".
type Config struct {
	Path string
	Args []string
	// CommandTransport mirrors upstream ShellConfig.commandTransport: "stdin"
	// writes the command to the shell's stdin instead of appending it to the
	// arguments (legacy WSL bash.exe). Empty means argv.
	CommandTransport string
}

var (
	legacyWSLBashPath = lazyregexp.New(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)
	driveRoot         = lazyregexp.New(`^[a-zA-Z]:[\\/]`)
)

// IsLegacyWSLBashPath mirrors upstream isLegacyWslBashPath: the path is the WSL launcher bash.exe in the System32 or
// Sysnative directory of the Windows directory in SystemRoot, which need not be C:\Windows.
//
// Ports packages/coding-agent/src/utils/shell.ts (isLegacyWslBashPath).
func IsLegacyWSLBashPath(path string) bool {
	return isLegacyWSLBashPath(path, os.Getenv("SystemRoot"))
}

// isLegacyWSLBashPath is [IsLegacyWSLBashPath] for a given SystemRoot. A SystemRoot that is not a drive path matches
// nothing; an empty one falls back to the conventional launcher paths, as upstream does for a missing one.
func isLegacyWSLBashPath(path, systemRoot string) bool {
	normalized := jsstring.ToLower(nodepath.Win32Normalize(path))
	if systemRoot != "" {
		if !driveRoot.MatchString(systemRoot) {
			return false
		}
		for _, directory := range [...]string{"System32", "Sysnative"} {
			if normalized == jsstring.ToLower(nodepath.Win32Join(systemRoot, directory, "bash.exe")) {
				return true
			}
		}
		return false
	}
	// Fall back to conventional launcher paths only when the Windows directory is unknown.
	return legacyWSLBashPath.MatchString(normalized)
}

// ForBash mirrors upstream getBashShellConfig: legacy WSL bash reads the
// command from stdin, every other bash takes it after -c.
//
// Ports packages/coding-agent/src/utils/shell.ts (getBashShellConfig).
func ForBash(shell string) Config {
	if IsLegacyWSLBashPath(shell) {
		return Config{Path: shell, Args: []string{"-s"}, CommandTransport: "stdin"}
	}
	return Config{Path: shell, Args: []string{"-c"}}
}
