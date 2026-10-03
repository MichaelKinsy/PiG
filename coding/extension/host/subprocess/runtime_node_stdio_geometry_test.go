package subprocess_test

import "testing"

// pi-btw sizes its overlay from process.stdout.rows, not factory tui.terminal.
// Pi's Node stdout carries the terminal geometry; a subprocess pipe must use
// the same host measurements, including resize events.
func TestNodeStdoutGeometryFollowsHostResize(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const { Runtime } = await import(new URL("../runtime.mjs", process.argv[2]));
const runtime = new Runtime("geometry.mjs");
runtime.ready = { mode: "tui", width: 170, height: 55 };
let resized = 0;
process.stdout.on("resize", () => { resized++; });
runtime.handleNotify({ method: "height_change", args: { height: 55 } });
assert.equal(process.stdout.rows, 55);
assert.equal(process.stdout.columns, 170);
runtime.handleNotify({ method: "width_change", args: { width: 90 } });
assert.equal(process.stdout.columns, 90);
assert.equal(process.stdout.rows, 55);
assert.equal(resized, 2);
runtime.handleNotify({ method: "width_change", args: { width: 90 } });
assert.equal(resized, 2, "packed siblings must not duplicate stdout resize events");
`)
}

func TestNodeHeaderAndFooterFollowHeightChanges(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const { Runtime } = await import(new URL("../runtime.mjs", process.argv[2]));
const runtime = new Runtime("geometry.mjs");
runtime.ready = { mode: "tui", width: 80, height: 24 };
runtime.conn = {};
const frames = new Map();
const builds = { header: 0, footer: 0 };
runtime.fireAndForget = (method, args) => frames.set(method, args.lines);
for (const kind of ["header", "footer"]) {
  runtime.ui[kind === "header" ? "setHeader" : "setFooter"]((tui) => {
    builds[kind]++;
    return { render: () => [kind + "=" + tui.terminal.rows] };
  });
}
for (const height of [13, 31]) {
  runtime.handleNotify({ method: "height_change", args: { height } });
  assert.deepEqual(frames.get("ui.setHeader"), ["header=" + height]);
  assert.deepEqual(frames.get("ui.setFooter"), ["footer=" + height]);
}
assert.deepEqual(builds, { header: 1, footer: 1 }, "resize must not rebuild the factories");
`)
}

func TestNodeSharedSurfaceRetirementDisposesOnlyMatchingComponent(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const { Runtime } = await import(new URL("../runtime.mjs", process.argv[2]));
for (const kind of ["header", "footer"]) {
  const runtime = new Runtime("geometry.mjs");
  runtime.conn = {};
  let lastFrame;
  runtime.fireAndForget = (_method, args) => { lastFrame = args; };
  const setter = kind === "header" ? "setHeader" : "setFooter";
  let disposed = 0;
  const factory = (tui) => ({
    render: () => [String(tui.terminal.rows)],
    dispose: () => { disposed++; },
  });
  runtime.ui[setter](factory);
  const oldId = lastFrame.surfaceId;
  const retire = (surfaceId) => runtime.handleNotify({ method: "ui.surface_retired", args: { kind, surfaceId } });
  retire(oldId);
  assert.equal(disposed, 1, "Pi disposes a replaced custom component");
  retire(oldId);
  assert.equal(disposed, 1, "retirement must not dispose twice");
  runtime.ui[setter](factory);
  const newId = lastFrame.surfaceId;
  assert.notEqual(newId, oldId);
  assert.equal(lastFrame.updateOnly, false, "an explicit setter replaces the shared slot");
  retire(oldId);
  runtime.handleNotify({ method: "height_change", args: { height: 13 } });
  assert.equal(disposed, 1, "late retirement must not dispose a new component");
  assert.equal(lastFrame.surfaceId, newId);
  assert.equal(lastFrame.updateOnly, true, "resize updates only the installed component");
  assert.deepEqual(lastFrame.lines, ["13"]);
}
`)
}
