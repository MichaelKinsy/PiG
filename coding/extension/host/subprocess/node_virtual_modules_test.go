//go:build !pig_strip_node_extensions

package subprocess

// pi: packages/coding-agent/src/core/extensions/virtual-modules.ts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi serves the VIRTUAL_MODULES table to extensions "in source and compiled binary runtimes" (core/extensions/virtual-modules.ts). PiG's
// compiled runtime is the tree materializeNodeRuntime publishes from the archive embedded in the binary, so the table's namespaces must
// reach an extension through that tree and not only through the checked-in sources: every specifier of Pi's own VIRTUAL_MODULES serves the
// same export names and kinds of values from the materialized runtime as from Pi (the comparison of
// TestNodeRuntimeVirtualModulesMatchPiNamespaces, run on the published tree).
func TestMaterializedNodeRuntimeServesPiVirtualModules(t *testing.T) {
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeNodeRuntime(t.Context(), t.TempDir(), runtimeRoot); err != nil {
		t.Fatalf("materialize the Node runtime: %v", err)
	}
	assertVirtualModulesMatchPi(t, runtimeRoot)
}

// Ports packages/coding-agent/test/suite/regressions/11156-pi-ai-models-virtual-module.test.ts on the materialized runtime: the table
// exposes the pi-ai /models entrypoint with a clampThinkingLevel function, and the legacy @mariozechner scope serves the very module the
// @earendil-works scope serves.
func TestMaterializedNodeRuntimeExposesPiAiModelsToExtensions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the loader fixture: %v", err)
	}
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeNodeRuntime(t.Context(), t.TempDir(), runtimeRoot); err != nil {
		t.Fatalf("materialize the Node runtime: %v", err)
	}
	script := `
import assert from "node:assert/strict";
const models = await import("@earendil-works/pi-ai/models");
assert.notEqual(models, undefined);
assert.equal(typeof models.clampThinkingLevel, "function");
const legacy = await import("@mariozechner/pi-ai/models");
assert.equal(legacy, models);
`
	cmd := exec.Command(node, "--import", registerLoaderURL(t, runtimeRoot), "--input-type=module", "--eval", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pi-ai/models virtual module from the materialized runtime: %v\n%s", err, output)
	}
}
