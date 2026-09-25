package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Width expectations follow Pi's resolveOverlayLayout: percentages use the
// terminal, minWidth applies before available-space clamping, and an explicitly
// supplied options object suppresses the component.width fallback.
func TestNodeCustomOverlaySizingDefaults(t *testing.T) {
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(runtimePath)}).String()
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
for (const [name, options, componentWidth, want] of [
  ["default", undefined, undefined, 80],
  ["component fallback", undefined, 42, 42],
  ["empty object suppresses fallback", {}, 42, 80],
  ["function returning undefined suppresses fallback", () => undefined, 42, 80],
  ["doom", {width: "75%%", maxHeight: "95%%", margin: {top: 1}}, undefined, 90],
  ["minWidth clamped after margins", {width: 20, minWidth: 200, margin: 2}, undefined, 116],
  ["percentage before margin", {width: "50%%", margin: {left: 10, right: 10}}, undefined, 60],
  ["zero width", {width: 0}, undefined, 1],
  ["invalid width", {width: "oops"}, undefined, 80],
]) {
  const runtime = new Runtime("/ext/geometry.mjs");
  runtime.ready = {width: 120, height: 40};
  let release;
  const frames = [];
  runtime.call = () => new Promise(resolve => { release = resolve; });
  runtime.notify = (method, args) => { if (method === "ui.custom.render") frames.push(args); };
  const pending = runtime.openCustomOverlay(() => ({width: componentWidth, render: w => [String(w)]}), {overlay: true, overlayOptions: options});
  for (let i = 0; i < 10 && !release; i++) await Promise.resolve();
  try {
    assert.equal(frames[0]?.lines[0], String(want), name);
    assert.equal(frames[0]?.width, 120, name + ": terminal width key");
  } finally { release({}); await pending; }
}
`, runtimeURL)
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("custom overlay sizing: %v\n%s", err, output)
	}
}
