// Run from the repository root: node test/parity/probes/overlay-shim.mjs
// Pi 1.2.0 interactive-mode.ts:2966-3043 (showExtensionCustom) and tui.ts:598-842 (setFocus, showOverlay, OverlayHandle,
// hideOverlay) own the observed contract.
//
// The probe runs Pi's unmodified overlay-qa-tests.ts commands twice through the existing Node loader, with the same keys:
// - Pi: the installed Pi's InteractiveMode.prototype.showExtensionCustom on Pi's TuiMainScreen (painting suppressed only).
// - PiG: the production Runtime.openCustomOverlay factory path. Its host calls (ui.custom, ui.custom.showOverlay, ui.custom.control,
//   ui.custom.hideOverlay, ui.custom.setFocus) are answered by a second Pi TuiMainScreen holding one placeholder per key, keys go to
//   the placeholder that holds focus as ui.custom.input, and focus changes reach the runtime as ui.custom.focus, as the PiG host
//   sends them. The Go host's side of the same calls is covered by the Go tests named in docs/findings/fix-overlay-shim.md.
// After every step both sides must show the same overlay stack: the same components in order, each rendering the same lines, the
// same one focused. Each PiG component's last frame must equal what it renders, so a redraw the runtime missed fails.
// setInterval is replaced by a manual clock and toLocaleTimeString by a constant, so timer and stream redraws are deterministic.
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { PI_PACKAGE_VERSION } from "../testdata/pi-version.mjs";

const temporary = mkdtempSync(join(tmpdir(), "pig-overlay-probe-"));
process.env.PIG_HOME = temporary;
process.env.PI_CODING_AGENT_DIR = join(temporary, "pi-agent");
process.on("exit", () => rmSync(temporary, { recursive: true, force: true }));
const fromRoot = (path) => pathToFileURL(resolve(path));
const runtimeRoot = fromRoot("coding/extension/host/subprocess/runtime-node/").href + "/";
const { Runtime } = await import(new URL("runtime.mjs", runtimeRoot));
const { importExtension } = await import(new URL("jiti-loader.mjs", runtimeRoot));
const piRoot = fromRoot("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/").href + "/";
assert.equal(JSON.parse(readFileSync(new URL("package.json", piRoot), "utf8")).version, PI_PACKAGE_VERSION);
const { InteractiveMode } = await import(new URL("dist/modes/interactive/interactive-mode.js", piRoot));
const { TuiMainScreen } = await import(new URL("node_modules/@earendil-works/pi-tui/dist/index.js", piRoot));
const entry = resolve(".upstream/current/packages/coding-agent/examples/extensions/overlay-qa-tests.ts");
const install = await importExtension(entry);
const runtime = new Runtime(entry);
runtime.ready = { width: 120, height: 40 };
await install(runtime.api);
runtime.commitLoad();

// A manual clock for the examples' setInterval, shared by both sides (only one command runs at a time).
const intervals = new Map();
let intervalSeq = 0;
globalThis.setInterval = (callback) => { intervals.set(++intervalSeq, callback); return intervalSeq; };
globalThis.clearInterval = (id) => { intervals.delete(id); };
Date.prototype.toLocaleTimeString = () => "12:00:00";

const settle = async () => {
  for (let i = 0; i < 3; i++) await new Promise(setImmediate);
  await new Promise((done) => setTimeout(done, 40)); // past the runtime's frame throttle
};
const newScreen = () => {
  const screen = new TuiMainScreen({ columns: 120, rows: 40, hideCursor() {} });
  screen.requestRender = () => {};
  return screen;
};

// Pi's side.
const piScreen = newScreen();
const piNotes = [];
const pi = {
  name: "Pi",
  ui: {
    setEditorText() {},
    notify: (message) => piNotes.push(message),
    custom: (...args) => InteractiveMode.prototype.showExtensionCustom.call({ ui: piScreen, editor: { getText: () => "" } }, ...args),
  },
  notes: piNotes,
  press(data) { piScreen.getFocusedComponent()?.handleInput(data); },
  stack: () => piScreen.overlayStack.map((entry) => ({ component: entry.component, hidden: entry.hidden, focused: piScreen.getFocusedComponent() === entry.component })),
  leaks: () => piScreen.overlayStack.length,
};

// PiG's side: the runtime with a host whose TUI is a Pi TuiMainScreen of placeholders.
const hostScreen = newScreen();
const placeholders = new Map(); // key -> { component, handle, options }
const frames = new Map();
const closers = new Map();
const published = new Map();
const pigNotes = [];
const failures = [];
const placeholder = (key) => ({ key, render: () => [], invalidate() {} });
const focusedKey = () => [...placeholders].find(([, mounted]) => hostScreen.getFocusedComponent() === mounted.component)?.[0] ?? "";
const stateOf = (key) => {
  const { handle, component } = placeholders.get(key);
  const mounted = hostScreen.overlayStack.some((entry) => entry.component === component);
  return { hidden: handle.isHidden(), focused: handle.isFocused(), visible: mounted && !handle.isHidden() };
};
// The PiG host publishes each focus change of a mounted component after the call that made it, from the component's input loop.
const publishFocus = () => setImmediate(() => {
  for (const [key, mounted] of placeholders) {
    const focused = hostScreen.getFocusedComponent() === mounted.component;
    if (published.get(key) === focused) continue;
    published.set(key, focused);
    runtime.handleNotify({ method: "ui.custom.focus", args: { key, focused } });
  }
});
const mount = (key, overlay, options) => {
  const component = placeholder(key);
  let handle;
  if (overlay) handle = hostScreen.showOverlay(component, options);
  else hostScreen.setFocus(component);
  placeholders.set(key, { component, handle, options });
  published.set(key, hostScreen.getFocusedComponent() === component);
  publishFocus();
};
const focusTarget = (target) => {
  if (!target || target.kind === "null") return null;
  if (target.kind === "overlay") return placeholders.get(target.key).component;
  throw new Error(`unexpected focus target ${JSON.stringify(target)}`);
};
runtime.callSync = (method, args) => {
  switch (method) {
    case "ui.custom.showOverlay":
      assert.ok(!placeholders.has(args.key), `key ${args.key} reused`);
      mount(args.key, true, args.overlayOptions);
      return { ok: true, ...stateOf(args.key), focusedKey: focusedKey() };
    case "ui.custom.control": {
      const { handle } = placeholders.get(args.key);
      if (args.action === "setHidden") handle.setHidden(args.hidden);
      else if (args.action === "unfocus" && args.target) handle.unfocus({ target: focusTarget(args.target) });
      else if (args.action) handle[args.action]();
      const answer = { ...stateOf(args.key), focusedKey: focusedKey() };
      if (args.action === "hide") placeholders.delete(args.key);
      publishFocus();
      return answer;
    }
    case "ui.custom.hideOverlay":
      hostScreen.hideOverlay();
      publishFocus();
      return { focusedKey: focusedKey() };
    case "ui.custom.setFocus":
      hostScreen.setFocus(focusTarget(args.target));
      publishFocus();
      return { focusedKey: focusedKey() };
    default:
      throw new Error(`unexpected synchronous host call ${method}`);
  }
};
runtime.call = (method, args) => {
  if (method !== "ui.custom") return Promise.reject(new Error(`unexpected host call ${method}`));
  mount(args.key, args.overlay, args.overlayOptions);
  if (args.hasHandle) runtime.handleNotify({ method: "ui.custom.opened", args: { key: args.key, ...stateOf(args.key) } });
  return new Promise((resolveCall) => closers.set(args.key, (result) => {
    // interactive-mode.ts:2997: an overlay's close pops the topmost overlay.
    if (args.overlay) hostScreen.hideOverlay();
    placeholders.delete(args.key);
    publishFocus();
    resolveCall({ ok: true, result });
  }));
};
runtime.notify = (method, args) => {
  if (method === "ui.custom.render") frames.set(args.key, args.lines);
  if (method === "ui.custom.close") {
    if (args.error) failures.push(args.error);
    closers.get(args.key)?.(args.result);
  }
};
runtime.fireAndForget = (method, args) => {
  if (method === "ui.notify") failures.push(args.message);
};
const pig = {
  name: "PiG",
  ui: {
    setEditorText() {},
    notify: (message) => pigNotes.push(message),
    custom: (...args) => runtime.openCustomOverlay(...args),
  },
  notes: pigNotes,
  press(data) {
    const key = focusedKey();
    if (key) runtime.handleNotify({ method: "ui.custom.input", args: { key, data, state: stateOf(key) } });
  },
  stack: () => hostScreen.overlayStack.map((entry) => {
    const key = entry.component.key;
    const component = runtime.customOverlays.get(key)?.component;
    assert.ok(component, `the host shows ${key}, which the runtime no longer owns`);
    const width = Math.min(placeholders.get(key)?.options?.width ?? 80, 120);
    assert.deepEqual(frames.get(key), component.render(width).map(String), `${key}'s last frame is not what it renders`);
    return { component, hidden: entry.hidden, focused: hostScreen.getFocusedComponent() === entry.component };
  }),
  leaks: () => hostScreen.overlayStack.length + placeholders.size + runtime.customOverlays.size,
};

// The runtime here has no host palette, so styling (SGR) differs; text, cursor markers, stack order and focus are compared.
const plain = (line) => String(line).replace(/\x1b\[[0-9;]*m/g, "");
const view = (side) => side.stack().map(({ component, hidden, focused }) => ({ hidden, focused, lines: component.render(40).map(plain) }));

async function run(side, name, steps) {
  const done = runtime.commands.get(name).handler("", { ui: side.ui });
  const seen = [];
  await settle();
  seen.push(view(side));
  for (const step of steps) {
    if (step === "tick") for (const callback of [...intervals.values()]) callback();
    else if (typeof step === "object") while (!side.notes.includes(step.notified)) await new Promise((wake) => setTimeout(wake, 10));
    else side.press(step);
    await settle();
    seen.push(view(side));
  }
  await done;
  await settle();
  assert.equal(side.leaks(), 0, `${side.name} ${name} leaked an overlay`);
  assert.equal(intervals.size, 0, `${side.name} ${name} left a timer running`);
  return seen;
}

const scenarios = {
  // Controller focused; Tab focuses Alpha; typing goes to the focused panel; Tab/Shift+Tab cycle; Ctrl+D and Esc dismiss a panel
  // with a targeted hide; Ctrl+C hides the rest and closes the controller.
  "overlay-focus": ["x", "\t", "a", "\t", "b", "\x1b[Z", "c", "\x04", "\t", "\x1b", "\x03"],
  // The non-capturing timer panel redraws when the controller's interval ticks; Esc hides it and closes the controller.
  "overlay-passive": ["h", "i", "tick", "\x7f", "tick", "\x1b"],
  // Streaming lines redraw the controller; Tab cycles controller -> A -> B -> C -> controller through focus and unfocus.
  "overlay-streaming": ["tick", "tick", "\t", "q", "\t", "w", "\t", "\t", "tick", "\t", "\x1b"],
  // Three ui.custom overlays stack; each Esc closes the topmost.
  "overlay-stack": [{ notified: "Showing overlay 3 (front)..." }, "\x1b", "\x1b", "\x1b"],
};
for (const [name, steps] of Object.entries(scenarios)) {
  piNotes.length = 0;
  pigNotes.length = 0;
  const reference = await run(pi, name, steps);
  const actual = await run(pig, name, steps);
  const mounted = Math.max(...reference.map((stack) => stack.length));
  for (let step = 0; step < reference.length; step++) {
    assert.deepEqual(actual[step], reference[step], `${name}: after step ${step} (${JSON.stringify(steps[step - 1] ?? "open")}) PiG's overlays differ from Pi's`);
  }
  assert.deepEqual(pigNotes, piNotes, `${name}: notifications differ`);
  assert.deepEqual(failures, [], `${name}: the runtime reported errors`);
  console.log(`${name}: ${reference.length} states equal, up to ${mounted} overlays shown`);
}
