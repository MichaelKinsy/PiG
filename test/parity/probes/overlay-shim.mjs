// Run from the repository root. This is a strict diagnostic probe, not a passing parity claim.
// Pi 0.87.1 interactive-mode.ts:2858-2936 and tui.ts:685-800 own the observed contract.
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const temporary = mkdtempSync(join(tmpdir(), "pig-overlay-probe-"));
process.env.PIG_HOME = temporary;
process.env.PI_CODING_AGENT_DIR = join(temporary, "pi-agent");
process.on("exit", () => rmSync(temporary, { recursive: true, force: true }));
const fromRoot = (path) => pathToFileURL(resolve(path));
const runtimeRoot = fromRoot("coding/extension/host/subprocess/runtime-node/").href + "/";
const { Runtime } = await import(new URL("runtime.mjs", runtimeRoot));
const { importExtension } = await import(new URL("jiti-loader.mjs", runtimeRoot));
const piRoot = fromRoot("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/").href + "/";
assert.equal(JSON.parse(readFileSync(new URL("package.json", piRoot), "utf8")).version, "0.99.2");
const { InteractiveMode } = await import(new URL("dist/modes/interactive/interactive-mode.js", piRoot));
const { TuiMainScreen } = await import(new URL("node_modules/@earendil-works/pi-tui/dist/index.js", piRoot));
const entry = resolve(".upstream/current/packages/coding-agent/examples/extensions/overlay-qa-tests.ts");
const install = await importExtension(entry);
const runtime = new Runtime(entry);
runtime.ready = { width: 120, height: 40 };
await install(runtime.api);
runtime.commitLoad();

// Disable painting only. Pi's actual mount, focus, handle and dismissal methods run unchanged.
const screen = new TuiMainScreen({ columns: 120, rows: 40, hideCursor() {} });
screen.requestRender = () => {};
const mode = { ui: screen, editor: { getText: () => "" } };
const piCustom = (...args) => InteractiveMode.prototype.showExtensionCustom.call(mode, ...args);
const commands = ["overlay-focus", "overlay-passive", "overlay-streaming"];
for (const name of commands) {
  const pending = runtime.commands.get(name).handler("", { ui: { setEditorText() {}, custom: piCustom } });
  await new Promise(setImmediate);
  const count = screen.overlayStack.length;
  console.log(`Pi ${name}: mounted=${count}`);
  assert.equal(count, name === "overlay-passive" ? 2 : 4);
  screen.getFocusedComponent().handleInput("\x03");
  await pending;
  assert.equal(screen.overlayStack.length, 0, `${name} leaked a panel`);
}

let front;
const frontNotified = new Promise(resolve => { front = resolve; });
const stacked = runtime.commands.get("overlay-stack").handler("", {
  ui: {
    custom: piCustom,
    notify(message) { if (message === "Showing overlay 3 (front)...") front(); },
  },
});
await frontNotified;
await new Promise(setImmediate);
assert.equal(screen.overlayStack.length, 3);
console.log("Pi overlay-stack: mounted=3 before any close");
for (let i = 0; i < 3; i++) {
  screen.getFocusedComponent().handleInput("\x1b");
  await new Promise(setImmediate);
}
await stacked;
assert.equal(screen.overlayStack.length, 0);

for (const name of commands) {
  try {
    await runtime.commands.get(name).handler("", { ui: { setEditorText() {}, custom: (...args) => runtime.openCustomOverlay(...args) } });
    console.log(`PiG ${name}: completed`);
  } catch (error) {
    console.error(`PiG ${name}: ${error.message}`);
    process.exitCode = 1;
  }
  assert.equal(runtime.customOverlays.size, 0, `${name} leaked runtime state`);
}
