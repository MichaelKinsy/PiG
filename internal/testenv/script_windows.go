//go:build windows

package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// needsInterpreter is true: Windows cannot run a script by its #! line.
const needsInterpreter = true

// posixTool finds name.exe in Git for Windows' bin directory, where Git
// installs bash and sh, and skips the test when Git for Windows is absent.
func posixTool(t testing.TB, name string) string {
	t.Helper()
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		path := filepath.Join(base, "Git", "bin", name+".exe")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Skipf("Git for Windows %s is not installed; repository scripts need its POSIX shell", name)
	return ""
}

// VerbatimArgs makes cmd pass cmd.Args exactly to an MSYS or Cygwin program
// such as Git for Windows bash. That runtime parses the command line itself:
// it reads single quotes as quoting and expands unquoted glob patterns, and
// Go quotes an argument only when it contains a space or tab. Every argument
// is therefore double-quoted. Inside double quotes the MSYS runtime turns
// every \\ into \ and \" into ", unlike the Microsoft C runtime, which
// collapses backslashes only before a quote; so every backslash is doubled
// and every quote is escaped.
func VerbatimArgs(cmd *exec.Cmd) {
	parts := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		var quoted strings.Builder
		quoted.WriteByte('"')
		for _, r := range arg {
			switch r {
			case '\\':
				quoted.WriteString(`\\`)
			case '"':
				quoted.WriteString(`\"`)
			default:
				quoted.WriteRune(r)
			}
		}
		quoted.WriteByte('"')
		parts[i] = quoted.String()
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = strings.Join(parts, " ")
}
