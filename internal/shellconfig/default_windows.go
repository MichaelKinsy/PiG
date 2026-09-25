//go:build windows

package shellconfig

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Default resolves bash on Windows, mirroring upstream getShellConfig's win32
// branch (shell.ts): Git Bash in the known install locations
// (%ProgramFiles%\Git\bin\bash.exe, then the x86 variant), then bash.exe on
// PATH (Cygwin, MSYS2, WSL), else a helpful error. cmd.exe and PowerShell are
// not substitutes for bash syntax.
func Default() (Config, error) {
	var searched []string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		p := base + `\Git\bin\bash.exe`
		searched = append(searched, "  "+p)
		if _, err := os.Stat(p); err == nil {
			return ForBash(p), nil
		}
	}
	if p, err := exec.LookPath("bash.exe"); err == nil {
		return ForBash(p), nil
	}
	return Config{}, fmt.Errorf(
		"No bash shell found. Options:\n"+
			"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
			"  3. Set shellPath in settings.json\n\n"+
			"Searched Git Bash in:\n%s", strings.Join(searched, "\n"))
}
