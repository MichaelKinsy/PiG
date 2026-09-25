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

	"github.com/MichaelKinsy/PiG/agent/harness"
)

const isWindows = true

func detachedProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

func hiddenProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// killProcessTree runs taskkill /F /T; a failed spawn is ignored.
var killProcessTree = func(pid int) error {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	command := exec.Command(filepath.Join(systemRoot, "System32", "taskkill.exe"), "/F", "/T", "/PID", strconv.Itoa(pid))
	command.SysProcAttr = hiddenProcessAttributes()
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
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
