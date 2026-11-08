package mcp

import (
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// Ports apps/p2/plugins/mcp/windows-command.ts: how a stdio server starts on a Windows environment. The pi-env daemon
// starts `.exe` and `.com` programs directly and refuses batch files (`npx` is `npx.cmd`), so every other command runs
// through `cmd.exe`, quoted as cross-spawn quotes it for Node on Windows.

// WindowsCommand is the argv to start and the variables to add to the program's environment.
type WindowsCommand struct {
	Argv []string
	Env  map[string]string
}

// commandVariable is the variable holding the quoted command line `cmd.exe` runs.
const commandVariable = "P2_MCP_COMMAND"

var (
	executable = regexp.MustCompile(`(?i)\.(?:com|exe)$`)
	cmdShim    = regexp.MustCompile(`(?i)node_modules[\\/].bin[\\/][^\\/]+\.cmd$`)
	// metaCharacters are cmd.exe's meta characters (cross-spawn's list, after robvanderwoude.com/escapechars.php).
	metaCharacters = regexp.MustCompile("([()\\][%!^\"`<>&|;, *?])")
)

// WindowsCommandOf is command with args as the daemon starts it on Windows: directly for an `.exe` or `.com` path, else
// through `cmd.exe /d /s /c`, which finds `npx.cmd` for `npx` through `PATHEXT` as a shell would.
//
// The daemon quotes every argv element itself (libuv's rules) and has no verbatim mode, so a command line in an argument
// would reach `cmd.exe` with its quotes escaped as `\"`, which `cmd.exe` does not understand. The command line goes in an
// environment variable instead, and `cmd.exe` gets `%P2_MCP_COMMAND%`, which needs no quoting: it expands the variable
// before it parses quotes and `^` escapes, so it reads the line as cross-spawn's verbatim one.
func WindowsCommandOf(command string, args []string) WindowsCommand {
	if executable.MatchString(command) {
		return WindowsCommand{Argv: append([]string{command}, args...), Env: map[string]string{}}
	}
	// A cmd-shim in node_modules\.bin passes its arguments through a second cmd.exe parse: their `^` escapes twice.
	quote := escapeArgument
	if cmdShim.MatchString(command) {
		quote = func(arg string) string { return escapeCommand(escapeArgument(arg)) }
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, escapeCommand(nodepath.Win32Normalize(command)))
	for _, arg := range args {
		parts = append(parts, quote(arg))
	}
	return WindowsCommand{
		Argv: []string{"cmd.exe", "/d", "/s", "/c", "%" + commandVariable + "%"},
		Env:  map[string]string{commandVariable: strings.Join(parts, " ")},
	}
}

// escapeCommand is the program's name for cmd.exe: every meta character escaped with `^` (cross-spawn's
// `escapeCommand`).
func escapeCommand(command string) string { return metaCharacters.ReplaceAllString(command, "^$1") }

// escapeArgument is one argument for cmd.exe and then the program's own parsing (qntm.org/cmd, as cross-spawn):
// backslashes before a quote or the end doubled, quotes escaped, the whole quoted, then every meta character escaped
// with `^`.
func escapeArgument(arg string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	backslashes := 0
	for i := range len(arg) {
		switch arg[i] {
		case '\\':
			backslashes++
			continue
		case '"':
			quoted.WriteString(strings.Repeat(`\`, 2*backslashes))
			quoted.WriteString(`\"`)
		default:
			quoted.WriteString(strings.Repeat(`\`, backslashes))
			quoted.WriteByte(arg[i])
		}
		backslashes = 0
	}
	quoted.WriteString(strings.Repeat(`\`, 2*backslashes))
	quoted.WriteByte('"')
	return escapeCommand(quoted.String())
}
