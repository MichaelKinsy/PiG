//go:build windows

package codingagent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// editorCommand runs the editor through the command shell, as upstream spawns
// it with shell: true on Windows: Node joins the command and its arguments
// with spaces and runs %ComSpec% /d /s /c "<line>" verbatim, so .cmd editors
// and %VAR% references work as they do at a prompt.
func editorCommand(ctx context.Context, name string, args []string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	line := strings.Join(append([]string{name}, args...), " ")
	cmd := exec.CommandContext(ctx, shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: shell + ` /d /s /c "` + line + `"`}
	return cmd
}
