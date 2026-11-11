//go:build !windows

package codingagent

import (
	"context"
	"os/exec"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// editorCommand runs the editor directly, as upstream spawns it without a
// shell outside Windows.
func editorCommand(ctx context.Context, name string, args []string) *exec.Cmd {
	return linkerexec.CommandContext(ctx, name, args...)
}

// editorSignal names the signal that ended the editor, as Node's close event reports it ("SIGTERM"), or "" when the editor was not signaled.
func editorSignal(err *exec.ExitError) string {
	status, ok := err.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	switch sig := status.Signal(); sig {
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGALRM:
		return "SIGALRM"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	default:
		return SignalName(sig)
	}
}
