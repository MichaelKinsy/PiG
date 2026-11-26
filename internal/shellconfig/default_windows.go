//go:build windows

package shellconfig

import (
	"context"
	"os"
	"os/exec"

	"github.com/MichaelKinsy/PiG/internal/nodefs"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// Default resolves bash on Windows, mirroring upstream getShellConfig's win32
// branch (shell.ts): Git Bash in the known install locations
// (%ProgramFiles%\Git\bin\bash.exe, then the x86 variant), then Git Bash of
// git.exe on PATH, then bash.exe on PATH (Cygwin, MSYS2, WSL), else a helpful
// error. cmd.exe and PowerShell are not substitutes for bash syntax.
//
// Ports packages/coding-agent/src/utils/shell.ts (getShellConfig, win32 branch).
func Default() (Config, error) {
	return windowsDefault(
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		nodefs.Access,
		FindExecutableOnPath,
		runCommand,
	)
}

// runCommand runs name with args and a 5s timeout, mirroring upstream's
// spawnSync(..., { encoding: "utf-8", timeout: 5000, windowsHide: true }).
func runCommand(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), findTimeout)
	defer cancel()
	//nolint:gosec // G204 (class a): name is git.exe resolved on PATH; args are fixed ["var", "GIT_SHELL_PATH"].
	command := exec.CommandContext(ctx, name, args...)
	nodespawn.HideWindow(command, nodespawn.Ignore, nodespawn.Pipe, nodespawn.Pipe)
	nodespawn.SetProgram(command)
	output, err := command.Output()
	if err != nil {
		return "", false
	}
	return string(output), true
}
