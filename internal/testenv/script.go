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
// line selects: bash or sh through Bash or Sh, and python3 or node as found
// on PATH. A script with any other interpreter skips the test on Windows.
func ScriptCommand(t testing.TB, path string, args ...string) *exec.Cmd {
	t.Helper()
	if !needsInterpreter {
		return exec.Command(path, args...)
	}
	interpreter := ShebangInterpreter(t, path)
	var program string
	switch interpreter {
	case "bash":
		program = Bash(t)
	case "sh":
		program = Sh(t)
	case "python3":
		program = python3(t)
	case "node":
		program = node(t)
	default:
		t.Skipf("%s: Windows cannot run a #!%s script", path, interpreter)
	}
	return exec.Command(program, append([]string{path}, args...)...)
}

// ShebangInterpreter is the program a script's #! line runs, with /usr/bin/env
// and the directory removed: "#!/usr/bin/env python3" is python3.
func ShebangInterpreter(t testing.TB, path string) string {
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

// managedEnvSignals name the variables a lane, CI or release host sets. Node-dependent tests must not pass silently there.
var managedEnvSignals = []string{"CI", "GITHUB_ACTIONS", "PIG_LANE_SLICE"}

func node(t testing.TB) string {
	t.Helper()
	path, err := exec.LookPath("node")
	return requireNode(t, path, err, os.Getenv)
}

// requireNode returns path when node was found. Without node it skips on a plain developer run, and fails in a lane or CI environment, where a silent skip would hide every node-dependent test.
func requireNode(t testing.TB, path string, err error, getenv func(string) string) string {
	t.Helper()
	if err == nil {
		return path
	}
	for _, name := range managedEnvSignals {
		if getenv(name) != "" {
			t.Fatalf("node is not on PATH (%v) and %s is set, so this host must run node-dependent tests instead of skipping them; put the mise node on PATH (export PATH=$HOME/.local/share/mise/shims:$PATH)", err, name)
		}
	}
	t.Skip("node is not on PATH")
	return ""
}
