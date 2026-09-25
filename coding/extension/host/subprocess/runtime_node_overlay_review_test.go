package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi showExtensionCustom resolves options after awaiting the component factory.
// A subprocess snapshot must also be republished at a new terminal width even
// when its text is unchanged: the host cannot paint an old-width snapshot.
func TestNodeCustomOverlayResolutionAndResize(t *testing.T) {
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(runtimePath)}).String()
	for _, mode := range []string{"factory-order", "resize-identical", "options-rejection"} {
		t.Run(mode, func(t *testing.T) {
			script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
const runtime = new Runtime("/ext/overlay.mjs");
runtime.ready = { width: 120, height: 40 };
const frames = [];
let releaseHost;
let opened;
runtime.call = (_method, args) => { opened = args; return new Promise(resolve => { releaseHost = resolve; }); };
runtime.notify = (method, args) => { if (method === "ui.custom.render") frames.push(args); };
const mode = %q;
if (mode === "options-rejection") {
  let built = false;
  await assert.rejects(runtime.openCustomOverlay(() => { built = true; return { render: () => [] }; }, {
    overlay: true, overlayOptions: () => { throw new Error("bad options"); }
  }), /bad options/);
  assert.equal(built, true, "factory must precede option evaluation");
  assert.equal(runtime.customOverlays.size, 0, "failed options leaked overlay state");
} else {
  let width;
  const pending = runtime.openCustomOverlay(async () => {
    await Promise.resolve();
    width = 42;
    return { render: w => mode === "resize-identical" ? ["constant"] : ["#".repeat(w)] };
  }, { overlay: true, overlayOptions: () => ({ width: mode === "resize-identical" ? 40 : width }) });
  for (let i = 0; i < 10 && !opened; i++) await Promise.resolve();
  assert.ok(opened, "overlay did not open");
  try {
    if (mode === "factory-order") {
      assert.equal(opened.overlayOptions.width, 42, "options evaluated before factory completed");
      assert.equal(frames[0].lines[0].length, 42);
      assert.equal(frames[0].width, 120, "frame key must be terminal width, not overlay width");
    } else {
      runtime.ready.width = 100;
      runtime.customOverlays.values().next().value.renderImmediate();
      assert.deepEqual(frames.map(frame => frame.width), [120, 100], "unchanged text suppressed new-width frame");
    }
  } finally { releaseHost({}); await pending; }
}
`, runtimeURL, mode)
			if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
				t.Fatalf("custom overlay contract: %v\n%s", err, output)
			}
		})
	}
}
