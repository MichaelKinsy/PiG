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
