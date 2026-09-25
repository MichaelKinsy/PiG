package testenv

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Bash returns the bash that runs repository scripts, and skips the test
// when the host has none. On Windows it is Git for Windows' bash: bash.exe
// on PATH is usually the WSL launcher, which runs the script inside a Linux
// distribution without the host's toolchains or files.
func Bash(t testing.TB) string {
	t.Helper()
	return posixTool(t, "bash")
}

// Sh returns the POSIX sh that runs repository scripts, found as Bash is.
func Sh(t testing.TB) string {
	t.Helper()
	return posixTool(t, "sh")
}

// ScriptCommand returns a command that runs the repository script at path
// with args. A POSIX host runs the script directly. Windows cannot run a
// script by its #! line, so there the command names the interpreter that
// line selects: bash or sh through Bash or Sh, and python3 as found on PATH.
// A script with any other interpreter skips the test on Windows.
func ScriptCommand(t testing.TB, path string, args ...string) *exec.Cmd {
	t.Helper()
	if !needsInterpreter {
		return exec.Command(path, args...)
	}
	interpreter := shebangInterpreter(t, path)
	var program string
	switch interpreter {
	case "bash":
		program = Bash(t)
	case "sh":
		program = Sh(t)
	case "python3":
		program = python3(t)
	default:
		t.Skipf("%s: Windows cannot run a #!%s script", path, interpreter)
	}
	return exec.Command(program, append([]string{path}, args...)...)
}

// shebangInterpreter is the program a script's #! line runs, with /usr/bin/env
// and the directory removed: "#!/usr/bin/env python3" is python3.
func shebangInterpreter(t testing.TB, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil && line == "" {
		t.Fatalf("read %s: %v", path, err)
	}
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#!"))
	if !strings.HasPrefix(line, "#!") || len(fields) == 0 {
		t.Fatalf("%s has no #! line", path)
	}
	program := fields[0]
	if strings.HasSuffix(program, "/env") && len(fields) > 1 {
		program = fields[1]
	}
	return program[strings.LastIndex(program, "/")+1:]
}

func python3(t testing.TB) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("python3 is not on PATH")
	return ""
}
