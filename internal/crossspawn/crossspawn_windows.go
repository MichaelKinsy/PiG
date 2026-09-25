//go:build windows

package crossspawn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var (
	// executableFile matches files CreateProcess starts directly
	// (cross-spawn lib/parse.js isExecutableRegExp).
	executableFile = regexp.MustCompile(`(?i)\.(?:com|exe)$`)
	// cmdShim matches npm's .cmd shims, whose arguments cmd.exe parses twice
	// (cross-spawn lib/parse.js isCmdShimRegExp).
	cmdShim = regexp.MustCompile(`(?i)node_modules[\\/]\.bin[\\/][^\\/]+\.cmd$`)
	// metaChars are the characters cmd.exe interprets (cross-spawn
	// lib/util/escape.js metaCharsRegExp).
	metaChars           = regexp.MustCompile("([()\\][%!^\"`<>&|;, *?])")
	backslashesQuote    = regexp.MustCompile(`(\\*)"`)
	trailingBackslashes = regexp.MustCompile(`(\\*)$`)
)

// command mirrors cross-spawn's parseNonShell: a command that resolves to a
// file other than .exe or .com runs as `cmd.exe /d /s /c "<command> <args>"`
// with the command and each argument escaped, and the command line passed
// verbatim.
func command(ctx context.Context, name string, args []string) *exec.Cmd {
	file, err := exec.LookPath(name)
	if err != nil || executableFile.MatchString(file) {
		return exec.CommandContext(ctx, name, args...)
	}
	double := cmdShim.MatchString(file)
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, escapeCommand(filepath.Clean(name)))
	for _, arg := range args {
		parts = append(parts, escapeArgument(arg, double))
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: shell + ` /d /s /c "` + strings.Join(parts, " ") + `"`}
	return cmd
}

// escapeCommand is cross-spawn lib/util/escape.js escapeCommand.
func escapeCommand(command string) string {
	return metaChars.ReplaceAllString(command, "^$1")
}

// escapeArgument is cross-spawn lib/util/escape.js escapeArgument: quote the
// argument for the C runtime, then escape cmd.exe metacharacters (twice when
// cmd.exe parses the line a second time).
func escapeArgument(arg string, doubleEscape bool) string {
	arg = backslashesQuote.ReplaceAllString(arg, `$1$1\"`)
	arg = trailingBackslashes.ReplaceAllString(arg, `$1$1`)
	arg = `"` + arg + `"`
	arg = metaChars.ReplaceAllString(arg, "^$1")
	if doubleEscape {
		arg = metaChars.ReplaceAllString(arg, "^$1")
	}
	return arg
}
