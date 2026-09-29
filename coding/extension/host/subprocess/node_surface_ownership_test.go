package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi changes the shared slots only for an explicit UI setter (interactive-mode.ts:2425-2495), not when an unrelated extension receives state.
func TestNodeSurfaceUpdatesDoNotClearUnownedSlots(t *testing.T) {
	nodeCellRequireNode(t)
	module := filepath.Join(findModuleRoot(t), "coding/extension/host/subprocess/runtime-node/runtime.mjs")
	script := `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { Runtime } = await import(pathToFileURL(process.argv[1]));
const runtime = new Runtime("/ext/passive.mjs");
assert.equal(runtime.footerDataProvider().getGitBranch(), null, "Pi reports null outside a repository");
runtime.conn = {};
const calls = [];
runtime.fireAndForget = (method, args) => calls.push({ method, args });
runtime.applyState({ hasUI: true, editorText: "first" });
runtime.renderSpecialSurface("footer");
runtime.renderSpecialSurface("header");
assert.deepEqual(calls, [], "unowned redraw must not clear another extension's surfaces");
for (const kind of ["footer", "header"]) {
  const setter = kind === "footer" ? "setFooter" : "setHeader";
  let disposed = 0;
  runtime.ui[setter](() => ({ render: () => [kind], invalidate() {}, dispose() { disposed++; } }));
  assert.equal(calls.at(-1).args.clear, false);
  assert.deepEqual(calls.at(-1).args.lines, [kind]);
  runtime.ui[setter](undefined);
  assert.equal(disposed, 1, "clearing must dispose the component's timers/subscriptions");
  assert.deepEqual(calls.at(-1), { method: "ui." + setter, args: { clear: true } });
  calls.length = 0;
  runtime.renderSpecialSurface(kind);
  assert.deepEqual(calls, [], "explicit clear must not become a persistent clearing factory");
}
`
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script, module).CombinedOutput(); err != nil {
		t.Fatalf("surface ownership: %v\n%s", err, out)
	}
}
