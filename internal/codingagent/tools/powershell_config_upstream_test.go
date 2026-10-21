package tools

import (
	"errors"
	"os/exec"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// powerShellConfigForPath is powerShellConfigFor with no environment and no installation directory present, so only the PATH
// lookup decides.
func powerShellConfigForPath(goos string, lookPath func(string) (string, error)) (ShellConfig, error) {
	return powerShellConfigFor(goos, lookPath, func(string) string { return "" }, func(string) bool { return false })
}

// Ports packages/coding-agent/test/powershell-config.test.ts (#9490): discover PowerShell outside PATH without assuming a
// drive or Windows directory name. Windows paths are joined with Windows rules, so the cases run on any host.
func TestUpstreamPowerShellConfig(t *testing.T) {
	const (
		pwsh7             = `D:\Applications\PowerShell\7\pwsh.exe`
		windowsPowerShell = `E:\WinNT\System32\WindowsPowerShell\v1.0\powershell.exe`
	)
	env := map[string]string{"ProgramFiles": `D:\Applications`, "SystemRoot": `E:\WinNT`}
	getenv := func(name string) string { return env[name] }
	// lookup answers `where <name>`; an absent entry is a failed lookup.
	lookup := func(found map[string]string) func(string) (string, error) {
		return func(name string) (string, error) {
			if path, ok := found[name]; ok {
				return path, nil
			}
			return "", exec.ErrNotFound
		}
	}
	existing := func(paths ...string) func(string) bool {
		return func(path string) bool { return slices.Contains(paths, path) }
	}

	t.Run("prefers PowerShell 7 on PATH over known installation locations", func(t *testing.T) {
		const shell = `E:\Tools\pwsh.exe`
		got, err := powerShellConfigFor("windows", lookup(map[string]string{"pwsh.exe": shell}), getenv, existing(shell, pwsh7, windowsPowerShell))
		if err != nil || got.Path != shell || !slices.Equal(got.Args, PowerShellArgs) {
			t.Fatalf("config = %+v, %v", got, err)
		}
	})
	t.Run("finds PowerShell 7 under ProgramFiles before Windows PowerShell on PATH", func(t *testing.T) {
		got, err := powerShellConfigFor("windows", lookup(map[string]string{"powershell.exe": windowsPowerShell}), getenv, existing(pwsh7, windowsPowerShell))
		if err != nil || got.Path != pwsh7 || !slices.Equal(got.Args, PowerShellArgs) {
			t.Fatalf("config = %+v, %v", got, err)
		}
	})
	t.Run("prefers Windows PowerShell on PATH over its system installation", func(t *testing.T) {
		const shell = `E:\Tools\powershell.exe`
		got, err := powerShellConfigFor("windows", lookup(map[string]string{"powershell.exe": shell}), getenv, existing(shell, windowsPowerShell))
		if err != nil || got.Path != shell || !slices.Equal(got.Args, PowerShellArgs) {
			t.Fatalf("config = %+v, %v", got, err)
		}
	})
	t.Run("finds Windows PowerShell under SystemRoot when PATH lookup is unavailable", func(t *testing.T) {
		got, err := powerShellConfigFor("windows", lookup(nil), getenv, existing(windowsPowerShell))
		if err != nil || got.Path != windowsPowerShell || !slices.Equal(got.Args, PowerShellArgs) {
			t.Fatalf("config = %+v, %v", got, err)
		}
	})
	t.Run("reports an error when no PowerShell installation exists", func(t *testing.T) {
		_, err := powerShellConfigFor("windows", lookup(nil), getenv, existing())
		if err == nil || err.Error() != "No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH." {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("skips the installation directories whose variables are unset", func(t *testing.T) {
		_, err := powerShellConfigFor("windows", lookup(nil), func(string) string { return "" }, existing(pwsh7, windowsPowerShell))
		if err == nil {
			t.Fatal("found a PowerShell with ProgramFiles and SystemRoot unset")
		}
	})
}

// The installation-directory candidates are Windows paths whatever the host joins with.
func TestPowerShellInstallationCandidatesUseWindowsPathRules(t *testing.T) {
	var probed []string
	_, _ = powerShellConfigFor("windows", func(string) (string, error) { return "", errors.New("not on PATH") },
		func(name string) string {
			return map[string]string{"ProgramFiles": `D:\Applications\`, "SystemRoot": "E:/WinNT"}[name]
		},
		func(path string) bool { probed = append(probed, path); return false })
	want := []string{
		nodepath.Win32Join(`D:\Applications\`, "PowerShell", "7", "pwsh.exe"),
		nodepath.Win32Join("E:/WinNT", "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
	}
	if !slices.Equal(probed, want) || want[0] != `D:\Applications\PowerShell\7\pwsh.exe` || want[1] != `E:\WinNT\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("probed %q, want %q", probed, want)
	}
}
