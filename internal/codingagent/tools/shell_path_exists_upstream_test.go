package tools

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// Ports packages/coding-agent/test/shell-path-exists.test.ts: the shell checks use access(F_OK), which accepts a runnable
// Microsoft Store alias that Windows' existsSync rejects (nodejs/node#36790). shellconfig.FindExecutableOnPath runs the real
// `where`, so the PATH cases drive its parsing and existence check (shellconfig.FirstMatch) with the output `where` would
// print. Whether the real Windows check accepts an alias is covered natively only (nodefs.TestAccessAcceptsReparsePointWithMissingTarget).
func TestUpstreamWindowsStoreShellAliases(t *testing.T) {
	const aliasesDir = `D:\Users\test\AppData\Local\Microsoft\WindowsApps`
	accessible := func(string) bool { return true }

	for _, executable := range []string{"bash.exe", "pwsh.exe"} {
		t.Run("accepts "+executable+" found on PATH", func(t *testing.T) {
			alias := aliasesDir + `\` + executable
			if got := shellconfig.FirstMatch(alias+"\r\n", true, accessible); got != alias {
				t.Fatalf("PATH result = %q, want %q", got, alias)
			}
			if executable != "pwsh.exe" {
				return
			}
			config, err := powerShellConfigForPath("windows", func(name string) (string, error) {
				if name == executable {
					return shellconfig.FirstMatch(alias+"\r\n", true, accessible), nil
				}
				return "", exec.ErrNotFound
			})
			if err != nil || config.Path != alias {
				t.Fatalf("config = %+v, %v", config, err)
			}
		})
	}

	t.Run("accepts an explicitly configured shell alias", func(t *testing.T) {
		alias := aliasesDir + `\bash.exe`
		config, err := getShellConfig(alias, func(path string) bool { return path == alias })
		if err != nil || config.Path != alias {
			t.Fatalf("config = %+v, %v", config, err)
		}
	})

	t.Run("still rejects PATH results that fail the existence check", func(t *testing.T) {
		const windowsPowerShell = `D:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
		outputs := map[string]string{"pwsh.exe": aliasesDir + `\pwsh.exe`, "powershell.exe": windowsPowerShell}
		exists := func(path string) bool { return path != aliasesDir+`\pwsh.exe` }
		config, err := powerShellConfigForPath("windows", func(name string) (string, error) {
			if path := shellconfig.FirstMatch(outputs[name], true, exists); path != "" {
				return path, nil
			}
			return "", exec.ErrNotFound
		})
		if err != nil || config.Path != windowsPowerShell {
			t.Fatalf("config = %+v, %v", config, err)
		}
	})
}

// The upstream "Unix shell existence checks" use access(F_OK) on every platform. /bin/bash and the PATH fallback are
// shellconfig.TestUnixShellConfig*; the custom path is here.
func TestUpstreamUnixShellExistenceChecks(t *testing.T) {
	t.Run("rejects an explicitly configured shell path that fails access(F_OK)", func(t *testing.T) {
		_, err := getShellConfig("/missing/bash", func(string) bool { return false })
		if err == nil || err.Error() != "Custom shell path not found: /missing/bash" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("accepts a configured shell path that passes", func(t *testing.T) {
		config, err := getShellConfig("/opt/bash", func(path string) bool { return path == "/opt/bash" })
		if err != nil || config.Path != "/opt/bash" || !slices.Equal(config.Args, []string{"-c"}) || strings.Contains(config.CommandTransport, "stdin") {
			t.Fatalf("config = %+v, %v", config, err)
		}
	})
}
