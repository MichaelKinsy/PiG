//go:build windows

package env

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

const isWindows = true

// detachedProcessAttributes is nil: upstream spawns the shell with detached
// false on win32.
func detachedProcessAttributes() *syscall.SysProcAttr { return nil }

// killProcessTree runs taskkill /F /T; a failed spawn is ignored.
var killProcessTree = func(pid int) error {
	command := taskkillCommand(pid)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

// taskkillCommand is upstream's taskkill spawn with stdio "ignore", detached:
// true, and windowsHide: true. Upstream joins process.env.SystemRoot ??
// "C:\\Windows", so only an unset SystemRoot takes the default.
func taskkillCommand(pid int) *exec.Cmd {
	systemRoot, ok := os.LookupEnv("SystemRoot")
	if !ok {
		systemRoot = `C:\Windows`
	}
	command := exec.Command(filepath.Join(systemRoot, "System32", "taskkill.exe"), "/F", "/T", "/PID", strconv.Itoa(pid))
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
	nodespawn.HideWindow(command, nodespawn.Ignore, nodespawn.Ignore, nodespawn.Ignore)
	return command
}

func platformShellConfig(ctx context.Context) (shellConfig, error) {
	var candidates []string
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		candidates = append(candidates, programFiles+`\Git\bin\bash.exe`)
	}
	if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
		candidates = append(candidates, programFilesX86+`\Git\bin\bash.exe`)
	}
	for _, candidate := range candidates {
		if pathExists(candidate) {
			return getBashShellConfig(candidate), nil
		}
	}
	if bash := findBashOnPath(ctx); bash != "" {
		return getBashShellConfig(bash), nil
	}
	searched := make([]string, len(candidates))
	for index, candidate := range candidates {
		searched[index] = "  " + candidate
	}
	return shellConfig{}, &harness.ExecutionError{
		Code: harness.ExecutionErrorShellUnavailable,
		Message: "No bash shell found. Options:\n" +
			"  1. Install Git for Windows: https://git-scm.com/download/win\n" +
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n" +
			"  3. Configure an explicit shellPath\n\n" +
			"Searched Git Bash in:\n" + strings.Join(searched, "\n"),
	}
}

func signalExitCode(syscall.WaitStatus) (int, bool) { return 0, false }
