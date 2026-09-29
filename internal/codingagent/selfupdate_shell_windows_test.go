//go:build windows

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi config.ts and package-manager-cli.ts run owner probes and update steps
// through spawnProcess, which supports npm.cmd/pnpm.cmd on Windows.
func TestSelfUpdateRunsWindowsCommandShims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager & shim.cmd")
	if err := os.WriteFile(path, []byte("@echo probe-ok\r\n@exit /b 0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := osCmdRunner{ctx: t.Context()}
	if out, err := runner.Output(path, "root", "-g"); err != nil || out != "probe-ok" {
		t.Fatalf("manager probe = %q, %v", out, err)
	}
	if err := RunPackageManagerUpdate(&SelfUpdateCommand{Command: path, Args: []string{"install", "--ignore-scripts", "package@1.2.3"}, Display: "test shim"}); err != nil {
		t.Fatalf("manager update: %v", err)
	}
}
