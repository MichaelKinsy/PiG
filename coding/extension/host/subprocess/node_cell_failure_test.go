package subprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's loader.ts records each failed extension and continues loading healthy
// siblings. Its in-process runtime has no socket launcher; both PiG launchers
// must surface a runtime failure rather than turn it into a successful exit.
func TestNodeCellRuntimeFailureExitsNonzero(t *testing.T) {
	nodeCellRequireNode(t)
	t.Setenv("PIG_EXT_SOCKET", "")
	t.Setenv("PIG_TEST_CELL_MISSING_SOCKET", "")
	entry, manifest := nodeCellFailureManifest(t, []string{"broken"})
	for _, launcher := range []struct{ file, arg string }{
		{"cli.mjs", entry},
		{"cell.mjs", manifest},
	} {
		t.Run(launcher.file, func(t *testing.T) {
			cmd := exec.CommandContext(testbudget.Context(t), "node", filepath.Join("runtime-node", launcher.file), launcher.arg)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Errorf("launcher error = %v, want exit 1; output: %s", err, out)
			}
			if !strings.Contains(string(out), "Error: PIG_EXT_SOCKET not set") || !strings.Contains(string(out), "at Runtime.connect") {
				t.Errorf("launcher lost runtime error/stack: %s", out)
			}
			if launcher.file == "cell.mjs" && !strings.Contains(string(out), `extension "broken"`) {
				t.Errorf("cell lost failed member identity: %s", out)
			}
		})
	}
}

func TestNodeCellReportsRuntimeFailuresBeforeHealthySiblingStops(t *testing.T) {
	nodeCellRequireNode(t)
	_, manifest := nodeCellFailureManifest(t, []string{"broken-a", "healthy", "broken-b"})
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cellPath, err := filepath.Abs("runtime-node/cell.mjs")
	if err != nil {
		t.Fatal(err)
	}
	// Inject failures at the runtime boundary without changing the production
	// cell join. The healthy member cannot finish until diagnostics are proved.
	// An event-loop turn drains rejection handlers, not a wall-clock sleep.
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { Runtime } = await import(pathToFileURL(%q));
let releaseHealthy, allStarted;
const started = new Promise(resolve => { allStarted = resolve; });
let count = 0;
Runtime.prototype.run = async function () {
  if (++count === 3) allStarted();
  if (this.name === "healthy") return new Promise(resolve => { releaseHealthy = resolve; });
  throw new Error(this.name + " runtime boom");
};
const diagnostics = [];
console.error = message => diagnostics.push(message);
process.argv[2] = %q;
let joined = false;
const cell = import(pathToFileURL(%q)).then(() => { joined = true; });
await started;
await new Promise(setImmediate);
assert.equal(joined, false, "cell must retain its healthy member");
assert.equal(diagnostics.length, 2, "both runtime failures must be reported while healthy member is alive");
for (const name of ["broken-a", "broken-b"]) {
  assert.ok(diagnostics.some(message => message.includes('extension "' + name + '"') && message.includes(name + " runtime boom") && message.includes("at Runtime.run")), diagnostics.join("\n"));
}
assert.equal(process.exitCode, 1, "cell must retain a failing lifecycle outcome");
releaseHealthy();
await cell;
assert.equal(process.exitCode, 1, "healthy completion must not erase failure");
process.exitCode = 0;
`, runtimePath, manifest, cellPath)
	cmd := exec.CommandContext(testbudget.Context(t), "node", "--input-type=module", "--eval", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cell runtime failure ordering: %v\n%s", err, out)
	}
}

func nodeCellFailureManifest(t *testing.T, names []string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "extension.mjs")
	if err := os.WriteFile(entry, []byte("export default function () {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var members []string
	for _, name := range names {
		members = append(members, fmt.Sprintf(`{"name":%q,"entry":%q,"sockEnv":"PIG_TEST_CELL_MISSING_SOCKET"}`, name, entry))
	}
	manifest := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifest, []byte("["+strings.Join(members, ",")+"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	return entry, manifest
}
