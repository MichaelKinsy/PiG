package subprocess_test

import "testing"

// Pi interactive-mode.ts:2918-2920 publishes the mounted overlay handle. A
// retained handle lets pi-btw reuse its overlay instead of opening a second one.
func TestNodeCustomOverlayPublishesMountedHandle(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const { Runtime } = await import(new URL("../runtime.mjs", process.argv[2]));
const runtime = new Runtime("overlay-owner.mjs");
runtime.ready = { width: 80, height: 24 };
let close;
const calls = [];
runtime.call = (method, args) => {
 calls.push([method, args]);
 if (method === "ui.custom") return new Promise(resolve => { close = resolve; });
 return Promise.resolve({ hidden: false, focused: true, visible: true });
};
runtime.callSync = (method, args) => {
 calls.push([method, args]);
 return { hidden: false, focused: true, visible: true };
};
runtime.notify = () => {};
let handle, done;
const opened = runtime.openCustomOverlay((_tui, _theme, _keys, finish) => {
 done = finish;
 return { focused: false, render: () => ["overlay"], dispose() {} };
}, { overlay: true, overlayOptions: { nonCapturing: true }, onHandle: value => { handle = value; value.focus(); } });
await new Promise(setImmediate);
assert.equal(handle, undefined, "handle cannot exist before the host mounts it");
runtime.handleNotify({ method: "ui.custom.opened", args: { key: "custom-1", hidden: false, focused: false, visible: true } });
assert.ok(handle, "onHandle was dropped");
assert.equal(handle.isFocused(), true);
await new Promise(setImmediate);
assert.ok(calls.some(([method, args]) => method === "ui.custom.control" && args.action === "focus"));
done(); close(); await opened;
assert.equal(handle.isFocused(), false);
assert.equal(handle.getBounds(), undefined);
handle.setHidden(true);
assert.equal(handle.isHidden(), true);
`)
}
