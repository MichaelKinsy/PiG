//go:build windows

package configvalue

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// runShellCommand runs payload as upstream executeCommandUncached does on
// win32: through the configured bash (getShellConfig: Git Bash, or WSL bash
// with the command on stdin), and only when no bash can be started, through
// execSync's default shell, %ComSpec% /d /s /c. It returns trimmed stdout; a
// non-zero exit or empty output is ("", false).
func runShellCommand(ctx context.Context, payload string) (string, bool) {
	if value, executed := runConfiguredShell(ctx, payload); executed {
		return value, value != ""
	}
	return runDefaultShell(ctx, payload)
}

// runConfiguredShell mirrors upstream executeWithConfiguredShell. executed is
// false when no bash was found or it could not be started, so the caller
// falls back to the default shell.
func runConfiguredShell(ctx context.Context, payload string) (value string, executed bool) {
	shell, err := shellconfig.Default()
	if err != nil {
		return "", false
	}
	args := shell.Args
	if shell.CommandTransport != "stdin" {
		args = append(append([]string{}, args...), payload)
	}
	cmd := exec.CommandContext(ctx, shell.Path, args...)
	if shell.CommandTransport == "stdin" {
		cmd.Stdin = strings.NewReader(payload)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return "", false
		}
		return "", true
	}
	return strings.TrimSpace(string(out)), true
}

// runDefaultShell mirrors upstream executeWithDefaultShell: Node's execSync
// runs %ComSpec% /d /s /c "<command>" on Windows.
func runDefaultShell(ctx context.Context, payload string) (string, bool) {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: shell + ` /d /s /c "` + payload + `"`}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(out))
	return value, value != ""
}
