//go:build windows

package shellconfig

import (
	"os"

	"github.com/MichaelKinsy/PiG/internal/nodefs"
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
