// Package shellconfig resolves the bash shell PiG runs commands through,
// mirroring upstream packages/coding-agent/src/utils/shell.ts getShellConfig
// and getBashShellConfig. Upstream never consults $SHELL: commands use bash
// syntax, so a login shell such as zsh or fish is not a substitute.
package shellconfig

import (
	"fmt"
	"os"
	"strings"

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

// gitForWindowsShellSuffix is the POSIX shell suffix Git for Windows reports
// from `git var GIT_SHELL_PATH`: `<root>\usr\bin\sh.exe`.
const gitForWindowsShellSuffix = `usr\bin\sh.exe`

// GitBashFromShellPath derives `<root>\bin\bash.exe` from the output of
// `git var GIT_SHELL_PATH` when it matches the Git for Windows layout
// (`<root>\usr\bin\sh.exe`) and the launcher exists according to exists.
//
// Ports packages/coding-agent/src/utils/shell.ts (findGitBashOfGitOnPath) and
// packages/durable/src/env/node.ts (findGitBashOfGitOnPath).
func GitBashFromShellPath(rawOutput string, exists func(string) bool) string {
	gitShell := nodepath.Win32Normalize(strings.TrimSpace(rawOutput))
	if !strings.HasSuffix(jsstring.ToLower(gitShell), `\`+gitForWindowsShellSuffix) {
		return ""
	}
	root := gitShell[:len(gitShell)-len(gitForWindowsShellSuffix)]
	bash := nodepath.Win32Join(root, "bin", "bash.exe")
	if exists(bash) {
		return bash
	}
	return ""
}

// windowsDefault resolves bash on Windows:
// 1. Git Bash under ProgramFiles / ProgramFiles(x86)
// 2. Git Bash of git.exe on PATH (`git var GIT_SHELL_PATH`, Git 2.47+)
// 3. bash.exe on PATH (Cygwin, MSYS2, WSL, etc.)
func windowsDefault(
	programFiles, programFilesX86 string,
	exists func(string) bool,
	findOnPath func(string) string,
	run func(string, ...string) (string, bool),
) (Config, error) {
	var searched []string
	for _, base := range []string{programFiles, programFilesX86} {
		if base == "" {
			continue
		}
		p := base + `\Git\bin\bash.exe`
		searched = append(searched, "  "+p)
		if exists(p) {
			return ForBash(p), nil
		}
	}
	// Git Bash installed elsewhere, found through git on PATH. This must precede
	// the generic bash.exe lookup, which usually finds the WSL launcher in System32 first.
	if git := findOnPath("git.exe"); git != "" {
		if out, ok := run(git, "var", "GIT_SHELL_PATH"); ok {
			if gitBash := GitBashFromShellPath(out, exists); gitBash != "" {
				return ForBash(gitBash), nil
			}
		}
	}
	if p := findOnPath("bash.exe"); p != "" {
		return ForBash(p), nil
	}
	return Config{}, fmt.Errorf(
		"No bash shell found. Options:\n"+
			"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
			"  3. Set shellPath in settings.json\n\n"+
			"Searched Git Bash in:\n%s\n"+
			"  the Git for Windows installation of git.exe on PATH", strings.Join(searched, "\n"))
}
