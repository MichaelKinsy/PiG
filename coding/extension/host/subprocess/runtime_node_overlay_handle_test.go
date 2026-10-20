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

// Pi's factory receives the TUI itself (interactive-mode.ts:3008): tui.showOverlay (tui.ts:729-826) shows a raw overlay and returns
// its handle at once, so overlay-qa-tests.ts's FocusDemoController shows non-capturing panels and focuses one inside the factory,
// before its own overlay is shown and takes focus from the panel. The runtime asks the host synchronously, in that order; the
// panel's frames, input and controls use its own key; its hide ends it without any ui.custom call; tui.hideOverlay and tui.setFocus
// reach the host too.
func TestNodeFactoryTuiShowOverlayHandleIsSynchronous(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const { Runtime } = await import(new URL("../runtime.mjs", process.argv[2]));
const runtime = new Runtime("overlay-owner.mjs");
runtime.ready = { width: 80, height: 24 };
const calls = [];
const frames = [];
let focusedKey = "";
let close;
runtime.call = (method, args) => {
 calls.push(method);
 assert.equal(method, "ui.custom");
 focusedKey = args.key;
 return new Promise(resolve => { close = resolve; });
};
runtime.callSync = (method, args) => {
 calls.push(method + (args.action ? ":" + args.action : ""));
 if (method === "ui.custom.showOverlay") {
  assert.deepEqual(args.overlayOptions, { width: 20, nonCapturing: true });
  return { ok: true, hidden: false, focused: false, visible: true, focusedKey };
 }
 if (method === "ui.custom.control" && args.action === "focus") { focusedKey = args.key; return { hidden: false, focused: true, visible: true, focusedKey }; }
 if (method === "ui.custom.control" && args.action === "hide") { focusedKey = "custom-1"; return { hidden: false, focused: false, visible: false, focusedKey }; }
 if (method === "ui.custom.setFocus") { assert.deepEqual(args.target, { kind: "overlay", key: "custom-2" }); focusedKey = "custom-2"; return { focusedKey }; }
 if (method === "ui.custom.hideOverlay") return { focusedKey };
 throw new Error("unexpected " + method);
};
runtime.notify = (method, args) => { if (method === "ui.custom.render") frames.push(args.key + ":" + args.lines.join("|")); };
const panel = { focused: false, keys: [], render: () => ["panel focused=" + panel.focused], handleInput(data) { panel.keys.push(data); } };
let handle, tui, done;
const opened = runtime.openCustomOverlay((value, _theme, _keys, finish) => {
 tui = value;
 done = finish;
 handle = tui.showOverlay(panel, { nonCapturing: true, width: 20 });
 assert.equal(handle.isFocused(), false);
 handle.focus();
 assert.equal(handle.isFocused(), true, "focus() inside the factory must apply before it returns");
 assert.equal(panel.focused, true);
 return { render: () => ["owner"], handleInput() {}, invalidate() {} };
}, { overlay: true });
assert.deepEqual(calls, ["ui.custom.showOverlay", "ui.custom.control:focus", "ui.custom"]);
assert.equal(handle.isFocused(), false, "the owner overlay takes focus from the panel when it is shown");
assert.equal(panel.focused, false);
assert.ok(frames.includes("custom-2:panel focused=false"), JSON.stringify(frames));
runtime.handleNotify({ method: "ui.custom.input", args: { key: "custom-2", data: "x", state: { hidden: false, focused: true, visible: true } } });
assert.deepEqual(panel.keys, ["x"]);
assert.equal(handle.isFocused(), true);
tui.setFocus(panel);
tui.hideOverlay();
handle.hide();
assert.equal(runtime.customOverlays.has("custom-2"), false, "a hidden raw overlay leaves the runtime");
assert.equal(handle.isFocused(), false);
handle.focus();
assert.deepEqual(calls.slice(3), ["ui.custom.setFocus", "ui.custom.hideOverlay", "ui.custom.control:hide"], "a hidden raw overlay makes no further host call");
done("ok");
close({ ok: true });
assert.equal(await opened, "ok");
assert.equal(runtime.customOverlays.size, 0);
`)
}

// Pi hands setWidget, setFooter and setHeader factories the TUI itself (interactive-mode.ts:2432, 2537, 2568), so their tui shows,
// hides and focuses overlays as a ui.custom factory's does, and its requestRender redraws the overlays the extension shows.
func TestNodeSurfaceFactoryTuiShowsOverlays(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const { Runtime } = await import(new URL("../runtime.mjs", process.argv[2]));
const runtime = new Runtime("surface-owner.mjs");
runtime.ready = { width: 80, height: 24 };
// A widget factory runs only where the TUI renders it (Pi's RPC mode ignores it).
runtime.contextValues.mode = "tui";
const calls = [];
const frames = [];
runtime.callSync = (method, args) => {
 calls.push(method + (args.action ? ":" + args.action : ""));
 if (method === "ui.custom.showOverlay") return { ok: true, hidden: false, focused: false, visible: true, focusedKey: "" };
 if (method === "ui.custom.control") return { hidden: false, focused: args.action === "focus", visible: args.action !== "hide", focusedKey: args.action === "focus" ? args.key : "" };
 return { focusedKey: "" };
};
runtime.call = async () => ({});
runtime.fireAndForget = () => {};
runtime.conn = {};
runtime.notify = (method, args) => { if (method === "ui.custom.render") frames.push(args.key + ":" + args.lines.join("|")); };
const tuis = {};
let ticks = 0;
const panel = { render: () => ["ticks=" + ticks], invalidate() {} };
runtime.ui.setWidget("w", tui => { tuis.widget = tui; return { render: () => ["widget"], invalidate() {} }; });
runtime.ui.setHeader(tui => { tuis.header = tui; return { render: () => ["header"], invalidate() {} }; });
runtime.ui.setFooter(tui => { tuis.footer = tui; return { render: () => ["footer"], invalidate() {} }; });
for (const [name, tui] of Object.entries(tuis)) {
 for (const member of ["showOverlay", "hideOverlay", "setFocus", "requestRender"]) assert.equal(typeof tui[member], "function", name + " tui lacks " + member);
 assert.equal(tui.terminal.columns, 80, name);
}
const handle = tuis.widget.showOverlay(panel, { nonCapturing: true });
handle.focus();
assert.equal(handle.isFocused(), true);
ticks = 1;
tuis.footer.requestRender();
await new Promise(resolve => setTimeout(resolve, 40));
assert.ok(frames.includes("custom-1:ticks=1"), "requestRender did not redraw the overlay: " + JSON.stringify(frames));
tuis.header.setFocus(null);
tuis.header.hideOverlay();
handle.hide();
assert.deepEqual(calls, ["ui.custom.showOverlay", "ui.custom.control:focus", "ui.custom.setFocus", "ui.custom.hideOverlay", "ui.custom.control:hide"]);
assert.equal(runtime.customOverlays.size, 0);
`)
}
