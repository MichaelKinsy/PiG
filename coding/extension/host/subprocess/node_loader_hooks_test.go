package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's virtual modules retain one namespace identity (loader.ts:490-501). Native imports and require must resolve to that namespace without the deprecated worker-thread registration API when synchronous hooks exist.
func TestNodeLoaderUsesSynchronousHooksWhenAvailable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	script := `import assert from "node:assert/strict";
import module from "node:module";
if (typeof module.registerHooks === "function") {
  module.register = () => { throw new Error("deprecated module.register called despite registerHooks support"); };
  module.syncBuiltinESMExports();
}
await import(process.argv[1]);
const current = await import("@earendil-works/pi-ai");
const legacy = await import("@mariozechner/pi-ai/compat");
assert.equal(current, legacy);
assert.equal(typeof current.calculateCost, "function");
assert.equal(typeof (await import("@earendil-works/pi-tui")).CancellableLoader, "function");
if (typeof module.registerHooks === "function") {
  assert.equal(module.createRequire(import.meta.url)("@earendil-works/pi-ai").calculateCost, current.calculateCost);
}
`
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "--eval", script, registerLoaderURL(t, runtimeRoot))
	if output, err := cmd.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatalf("loader hook selection: %v\n%s", err, output)
	}
}

func TestNodeIssuePackageForms(t *testing.T) {
	t.Parallel()
	entry := filepath.Join(findModuleRoot(t), "test/parity", "scenarios", "extensions-runtime", "testdata", "issue-package")
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			got := loadNodeCommandDescription(t, NewHostWithConfigRoot(t.TempDir(), t.TempDir()), entry, isolation, "issue-package")
			want := `{"phase":"loaded","loader":"function","cost":{"input":2,"output":4,"cacheRead":0.19999999999999998,"cacheWrite":0.3,"total":6.5}}`
			if got != want {
				t.Fatalf("package factory = %s, want Pi's %s", got, want)
			}
		})
	}
}
