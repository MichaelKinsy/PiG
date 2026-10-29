//go:build windows

package crossspawn

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

func command(ctx context.Context, dir, name string, args []string) *exec.Cmd {
	plan := planWindowsCommand(dir, name, args)
	//nolint:gosec // G204 (class c): generic spawn wrapper: the callers pass the user's npmCommand and git settings, an MCP server's configured command, or PiG's own self-update tools; the plan resolves .cmd and .bat launchers as cross-spawn does.
	cmd := exec.CommandContext(ctx, plan.name, plan.args...)
	if plan.path != "" {
		// Resolve against the child cwd, not CommandContext's parent cwd.
		cmd.Path = plan.path
		cmd.Err = nil
	}
	if plan.cmdLine != "" {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: plan.cmdLine}
	} else {
		nodespawn.SetCommandLine(cmd)
	}
	return cmd
}

// ShellCommand interprets source through Node's default Windows shell,
// ComSpec (cmd.exe by default). Only use this for an intentional shell program,
// never for a filename, URL, or argv.
func ShellCommand(ctx context.Context, source string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	plan := planWindowsShell(shell, source)
	//nolint:gosec // G204 (class c): the shell is ComSpec (default cmd.exe) and source is a command the user wrote in settings or config; the function is for an intentional shell program and never for a filename, URL or argv.
	cmd := exec.CommandContext(ctx, plan.name, plan.args...)
	if plan.cmdLine != "" {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: plan.cmdLine}
	} else {
		nodespawn.SetCommandLine(cmd)
	}
	return cmd
}
