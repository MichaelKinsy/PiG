// Package shellconfig resolves the bash shell PiG runs commands through,
// mirroring upstream packages/coding-agent/src/utils/shell.ts getShellConfig
// and getBashShellConfig. Upstream never consults $SHELL: commands use bash
// syntax, so a login shell such as zsh or fish is not a substitute.
package shellconfig

import (
	"regexp"
	"strings"
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

var legacyWSLBashPath = regexp.MustCompile(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

// IsLegacyWSLBashPath mirrors upstream isLegacyWslBashPath.
func IsLegacyWSLBashPath(path string) bool {
	return legacyWSLBashPath.MatchString(strings.ToLower(strings.ReplaceAll(path, "/", `\`)))
}

// ForBash mirrors upstream getBashShellConfig: legacy WSL bash reads the
// command from stdin, every other bash takes it after -c.
func ForBash(shell string) Config {
	if IsLegacyWSLBashPath(shell) {
		return Config{Path: shell, Args: []string{"-s"}, CommandTransport: "stdin"}
	}
	return Config{Path: shell, Args: []string{"-c"}}
}
