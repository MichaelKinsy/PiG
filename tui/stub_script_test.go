package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// stubLauncherSource runs the shell script named by its second verb with the
// shell named by its first, forwarding arguments, stdio, and the exit code.
const stubLauncherSource = `package main

import (
	"errors"
	"os"
	"os/exec"
)

func main() {
	cmd := exec.Command(%q, append([]string{%q}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		os.Exit(127)
	}
}
`

// writeStubScript writes a POSIX shell stub at path and returns the path that
// starts it. Windows starts only files with an executable extension, and a
// .cmd wrapper misparses quoted arguments when its own path contains spaces,
// so there the script is written beside a path.exe launcher that runs it with
// Git for Windows' sh.exe.
func writeStubScript(t *testing.T, path, script string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var sh string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		candidate := filepath.Join(base, "Git", "bin", "sh.exe")
		if _, err := os.Stat(candidate); base != "" && err == nil {
			sh = candidate
			break
		}
	}
	if sh == "" {
		t.Fatal("Git for Windows sh.exe not found; the stub needs a POSIX shell")
	}
	if err := os.WriteFile(path+".sh", []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module stub\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), fmt.Appendf(nil, stubLauncherSource, sh, path+".sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := path + ".exe"
	build := exec.Command("go", "build", "-o", exe, ".")
	build.Dir = src
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stub launcher: %v\n%s", err, output)
	}
	return exe
}
