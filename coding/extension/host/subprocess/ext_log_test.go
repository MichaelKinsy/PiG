package subprocess

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withExtLog names the process's log and replaces an entry the host's own environment carries (an extension that starts a pig passes its own).
func TestWithExtLogReplacesAnInheritedEntry(t *testing.T) {
	env := []string{"A=1", "PIG_EXT_LOG=/stale", "B=2"}
	got := withExtLog(env, "/new.log")
	want := []string{"A=1", "B=2", "PIG_EXT_LOG=/new.log"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("withExtLog = %q, want %q", got, want)
	}
	if env[1] != "PIG_EXT_LOG=/stale" {
		t.Errorf("withExtLog changed its argument: %q", env)
	}
	if got := withExtLog(env, ""); strings.Contains(strings.Join(got, "\n"), "PIG_EXT_LOG") {
		t.Errorf("an empty path left an entry: %q", got)
	}
}

// The host's handle writes to the end, as the SDK's appends do: a handle with its own offset would overwrite what an SDK wrote to the same file.
func TestExtLogHostWritesDoNotOverwriteAnSDKAppend(t *testing.T) {
	file, err := createExtLog("pig-ext-log-test-*.log")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	t.Cleanup(func() { _ = os.Remove(path) })
	if _, err := file.WriteString("host one\n"); err != nil {
		t.Fatal(err)
	}
	sdk, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.WriteString("sdk diagnostic\n"); err != nil {
		t.Fatal(err)
	}
	_ = sdk.Close()
	if _, err := file.WriteString("host two\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "host one\nsdk diagnostic\nhost two\n"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

// Fused extensions share the host's environment, so the host exports one log for them and removes it with the Host.
func TestFusedExtLogIsExportedAndRemoved(t *testing.T) {
	t.Setenv(extLogEnvName, "")
	var log fusedExtLog
	log.export()
	path := os.Getenv(extLogEnvName)
	if path == "" || !strings.HasPrefix(filepath.Base(path), "pig-ext-fused-") {
		t.Fatalf("PIG_EXT_LOG = %q after export", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the log does not exist: %v", err)
	}
	log.export()
	if again := os.Getenv(extLogEnvName); again != path {
		t.Errorf("a second export chose %q, want the same log %q", again, path)
	}
	log.remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the log remains after remove: %v", err)
	}
	if got := os.Getenv(extLogEnvName); got != "" {
		t.Errorf("PIG_EXT_LOG = %q after remove, want it unset", got)
	}
}

// The Node runtime writes its diagnostics to the log, one whole write each after the host's own, and to stderr when there is no log.
func TestNodeRuntimeDiagnosticsGoToTheExtensionLog(t *testing.T) {
	nodeCellRequireNode(t)
	module, err := filepath.Abs("runtime-node/diagnostic.mjs")
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "ext.log")
	if err := os.WriteFile(log, []byte("host wrote this line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const { diagnose, resetDiagnosticBytes, DIAGNOSTIC_LOG_MAX } = await import(pathToFileURL(%q));
const log = %q;
diagnose("pig: terminal input handler failed: Error: probe-panic-boom\n    at probe\n", log);
assert.equal(readFileSync(log, "utf8"), "host wrote this line\npig: terminal input handler failed: Error: probe-panic-boom\n    at probe\n");
resetDiagnosticBytes();
const chunk = "x".repeat(1 << 16) + "\n";
for (let i = 0; i < (2 * DIAGNOSTIC_LOG_MAX) / chunk.length; i++) diagnose(chunk, log);
const text = readFileSync(log, "utf8");
assert.ok(text.length <= DIAGNOSTIC_LOG_MAX + 1024 + 200, "the log is " + text.length + " bytes");
assert.equal(text.split("further SDK diagnostics dropped").length - 1, 1);
diagnose("to stderr\n", "");
`, module, log)
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("node: %v\n%s\n%s", err, out, stderr.String())
	}
	if got := stderr.String(); got != "to stderr\n" {
		t.Errorf("stderr = %q, want only the diagnostic that had no log", got)
	}
}
