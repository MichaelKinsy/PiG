//go:build !pig_strip_node_extensions

package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi 5b5be2c316 (#12145): ToolExecutionComponent.render appended the rows of a tool that draws itself (renderShell "self") with
// lines.push(...contentLines), which fails with "RangeError: Maximum call stack size exceeded" from about 125 000 rows on. PiG's own
// screen draws tool cards in Go and never had the limit, but the vendored runtime exports Pi's component to Node extensions
// (shims/pi-dist/pi-coding-agent/index.js), so an extension that draws one with a very large self-rendered result failed in it. The
// vendored file carries Pi's loop (automation/gen/pi-post-release-backports.mjs, 5b5be2c316e0): every row arrives, in order, after the blank row.
func TestNodeToolExecutionComponentDrawsVeryLargeSelfRenderedRowsPi5b5be2c31(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	root := filepath.Join(moduleRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	entry := filepath.Join(t.TempDir(), "large-card.ts")
	write(t, entry, `import { ToolExecutionComponent } from "@earendil-works/pi-coding-agent";
export default (rows: number) => {
  const lines = Array.from({ length: rows }, (_, i) => "row " + i);
  const definition = {
    name: "bulk", label: "bulk", description: "draws its own rows", parameters: { type: "object", properties: {} }, renderShell: "self",
    execute: async () => ({ content: [], details: undefined }),
    renderCall: () => ({ render: () => lines, invalidate() {} }),
  };
  const card = new ToolExecutionComponent("bulk", "bulk-1", {}, {}, definition as any, { requestRender() {} } as any, process.cwd());
  return card.render(100);
};`)
	script := `import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const root = pathToFileURL(process.argv[1] + "/");
const { Runtime } = await import(new URL("runtime.mjs", root));
const { importExtension } = await import(new URL("jiti-loader.mjs", root));
new Runtime(process.argv[2]);
const draw = await importExtension(process.argv[2]);
// 1.5 times the row count at which V8 rejects the spread.
const rows = 190000;
const lines = draw(rows);
assert.equal(lines.length, rows + 1);
assert.equal(lines[0], "");
assert.equal(lines[1], "row 0");
assert.equal(lines[rows], "row " + (rows - 1));
`
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "--eval", script, root, entry)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a self-rendered tool card of 190000 rows in the vendored runtime: %v\n%s", err, output)
	}
}
