package subprocess

import (
	"os/exec"
	"testing"
)

// Exercise the deployed runtime, not the source tree: Pi's utils module imports
// get-east-asian-width when an extension imports pi-tui.
func TestEmbeddedNodeRuntimeWidthUtilities(t *testing.T) {
	runtimeRoot := t.TempDir()
	if err := copyEmbeddedTree(nodeRuntimeFS, "runtime-node", runtimeRoot); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node",
		"--import", registerLoaderURL(t, runtimeRoot),
		"--input-type=module", "--eval", `
import assert from "node:assert/strict";
import { visibleWidth, truncateToWidth, wrapTextWithAnsi } from "@earendil-works/pi-tui";
assert.equal(visibleWidth("👨‍👩‍👧‍👦日本"), 6);
assert.equal(truncateToWidth("日本語", 4, ""), "日本\x1b[0m");
assert.deepEqual(wrapTextWithAnsi("日本語", 4), ["日本", "語"]);
`)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("embedded pi-tui width utilities: %v\n%s", err, output)
	}
}
