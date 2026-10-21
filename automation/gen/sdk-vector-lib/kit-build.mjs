// Builds the Pi components a component-kit view describes (docs/plan/extension-component-kit.md §2) from the
// view's wire JSON and renders them with Pi itself, so the goldens the hosts and the SDKs are tested against are
// Pi's own bytes. A case's `root` is the wire node an SDK sends (the same JSON the host decodes); `steps` are the
// keys and mouse events delivered to the component after it first rendered; the result holds the rendered rows of
// the initial state and after each step, at every width, under Pi's global theme and keybindings.
import { useTheme } from "./theme-state.mjs";

export const WIDTHS = [30, 72, 120];

// loadAll returns Pi's namespaces after putting the process-global state a render reads in place: the coding-agent
// theme (initTheme) and the app keybindings the message components' hints name.
export async function loadAll(env, themeName = "dark", colorMode = "truecolor") {
  const palette = await useTheme(env, themeName, colorMode);
  const tui = await env.load("pi-tui");
  const pi = await env.load("pi-coding-agent");
  // pi-coding-agent's KeybindingsManager is not a runtime export; the app entries the components' hints name are
  // the defaults of core/keybindings.js (KEYBINDINGS) added to pi-tui's own table, as the interactive mode installs.
  const defs = { ...tui.TUI_KEYBINDINGS, "app.tools.expand": { defaultKeys: "ctrl+o", description: "Toggle tool output" } };
  tui.setKeybindings(new tui.KeybindingsManager(defs, {}));
  return { tui, pi, palette };
}

// The coding-agent theme Pi's global holds.
export const piTheme = () => globalThis[Symbol.for("@earendil-works/pi-coding-agent:theme")];

// A theme token's style function, as the host builds it from the surface's theme; the identity for no token.
export function fg(token) {
  return token ? (text) => piTheme().fg(token, text) : (text) => text;
}

// A TUI that only counts the renders a component asks for.
export const fakeTui = (rows = 24) => ({ requestRender() {}, terminal: { rows, columns: 120 } });

// builders maps a node kind to (lib, node, build) => component. Each kind's case source registers its own.
export const builders = {
  container: (lib, n, build) => {
    const c = new lib.tui.Container();
    for (const child of n.children ?? []) c.addChild(build(child));
    return c;
  },
  text: (lib, n) => new lib.tui.Text(n.text ?? "", n.paddingX ?? 1, n.paddingY ?? 1),
  spacer: (lib, n) => new lib.tui.Spacer(n.lines ?? 1),
  "truncated-text": (lib, n) => new lib.tui.TruncatedText(n.text ?? "", n.paddingX ?? 0, n.paddingY ?? 0),
  markdown: (lib, n) => new lib.tui.Markdown(n.text ?? "", n.paddingX ?? 0, n.paddingY ?? 0, lib.pi.getMarkdownTheme()),
};

export function buildTree(lib, node, registry = builders) {
  const build = (child) => buildTree(lib, child, registry);
  const make = registry[node.kind];
  if (!make) throw new Error(`kit golden: no builder for ${node.kind}`);
  return make(lib, node, build);
}

// The mouse steps' dispatch results are the frame's `results`, in order.
// A step is { keys: "…" } or { mouse: { type, button, x, y, … } } (the local cell of the root component).
async function applyStep(lib, root, rows, width, step) {
  if (step.keys !== undefined) {
    // The focused node takes the keys; a case names it with `focus` (the node's id) through its builder's registry.
    if (root.__focus?.handleInput) root.__focus.handleInput(step.keys);
    return;
  }
  const m = step.mouse;
  const event = { type: m.type, button: m.button ?? "none", x: m.x, y: m.y, screenX: m.screenX ?? m.x, screenY: m.screenY ?? m.y, width, height: rows.length, shift: !!m.shift, alt: !!m.alt, ctrl: !!m.ctrl };
  if (m.wheelDelta !== undefined) event.wheelDelta = m.wheelDelta;
  if (m.clickCount !== undefined) event.clickCount = m.clickCount;
  // The frame's mouseResults: what the renderer would act on, "h" handled, "c" capture, "f" focus ("-" for none).
  const result = root.handleMouse?.(event);
  const flags = result ? `${result.handled ? "h" : ""}${result.capture ? "c" : ""}${result.focus ? "f" : ""}` : "";
  lib.results.push(flags || "-");
}

// renderCase builds the case's tree afresh for each width, renders it, applies the steps in order and renders
// after each.
export async function renderCase(env, spec, registry = builders, themeName = "dark", colorMode = "truecolor") {
  const lib = await loadAll(env, themeName, colorMode);
  const frames = [];
  for (const width of spec.widths ?? WIDTHS) {
    lib.ids = {};
    lib.events = [];
    lib.changes = [];
    lib.results = [];
    lib.timers = [];
    lib.focus = spec.focus;
    // The terminal's rows: an editor shows max(5, floor(rows * 0.3)) lines.
    lib.rows = spec.rows ?? 24;
    lib.probe = undefined;
    lib.probeInitial = false;
    const root = buildTree(lib, spec.root, registry);
    if (spec.focus) root.__focus = lib.ids[spec.focus];
    let rows = root.render(width);
    const states = [rows];
    // A case whose probe reads state the first render sets (a scroll view's layout) records it before the steps.
    if (lib.probe && lib.probeInitial) lib.changes.push(lib.probe());
    for (const step of spec.steps ?? []) {
      await applyStep(lib, root, rows, width, step);
      // A `settle` case lets the asynchronous work a step started (an editor's autocomplete query) finish before it renders.
      if (spec.settle) await new Promise((resolve) => setTimeout(resolve, 100));
      rows = root.render(width);
      states.push(rows);
      if (lib.probe) lib.changes.push(lib.probe());
    }
    // Loaders animate on a timer; the case drives its frames itself.
    for (const stop of lib.timers) stop();
    frames.push({ width, states, events: lib.events, changes: lib.changes, results: lib.results });
  }
  return { palette: lib.palette, frames };
}
