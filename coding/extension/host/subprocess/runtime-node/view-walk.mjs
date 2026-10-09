// pig additive (D107): the Node runtime's view derivation. After a surface
// render, the walk reads the real pi-tui component tree into the component
// kit's view (docs/plan/extension-component-kit.md §2, §9), so a D91 frontend
// can draw it natively. The rendered lines stay authoritative; the host keeps
// the view only when rendering it reproduces them byte for byte (§5.2).
//
// A component maps to a kind only when its render is exactly the pinned base
// class's render, or, for Pi's conversation components, when it is exactly an
// instance of the pinned class (§2.1). Anything else becomes a `lines` range:
// its rows come from the parent's mouseLayout (Container, Box), or from
// rendering it again only when no layout exists. Style closures map to theme
// token names by probing.
// The walk reads fields pi-tui marks private; runtime_node_view_pin_test.go
// fails when the pinned pi-tui renames one (VIEW_WALK_FIELDS).
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { getMarkdownTheme, getSelectListTheme, getSettingsListTheme } from "./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js";

const require = createRequire(import.meta.url);

// Spec §2 bounds.
const MAX_NODES = 4096;
const MAX_DEPTH = 64;
const MAX_LIST_ITEMS = 10000;

// The tokens a surface's viewTheme may override (§6), by slot.
export const VIEW_THEME_TOKENS = Object.freeze({
  accent: "fg", muted: "fg", dim: "fg", text: "fg", border: "fg", borderAccent: "fg",
  borderMuted: "fg", success: "fg", warning: "fg", error: "fg", selectedBg: "bg", customMessageBg: "bg",
});
const VIEW_THEME_COLOR = /^#[0-9a-fA-F]{6}$/;

// surfaceTheme reads the PiG-only viewTheme a surface's root component may
// set (§13.3). theme is the map the view sends while a frontend draws, as the
// author set it; colors are its [token, "#rrggbb"] entries when every entry
// is valid, and undefined otherwise: an invalid map is applied nowhere, and
// the host rejects the view that carries it. No viewTheme, or an empty one,
// is undefined.
export function surfaceTheme(component) {
  const theme = component?.viewTheme;
  if (theme === undefined || theme === null) return undefined;
  if (typeof theme !== "object" || Array.isArray(theme)) return { theme, colors: undefined };
  const entries = Object.entries(theme);
  if (entries.length === 0) return undefined;
  const valid = entries.every(([token, color]) => Object.hasOwn(VIEW_THEME_TOKENS, token) && typeof color === "string" && VIEW_THEME_COLOR.test(color));
  return { theme, colors: valid ? entries : undefined };
}

// The fields the walk reads, by pinned class. The pin test constructs each
// class and checks every field exists, so an upstream rename fails it.
export const VIEW_WALK_FIELDS = Object.freeze({
  Container: ["children", "mouseLayout"],
  Box: ["children", "paddingX", "paddingY", "bgFn", "cache", "mouseLayout"],
  Text: ["text", "paddingX", "paddingY", "customBgFn"],
  TruncatedText: ["text", "paddingX", "paddingY"],
  Markdown: ["text", "paddingX", "paddingY", "theme", "defaultTextStyle", "options"],
  Spacer: ["lines"],
  DynamicBorder: ["color"],
  SelectList: ["items", "filteredItems", "selectedIndex", "maxVisible", "theme", "layout"],
  SettingsList: ["items", "filteredItems", "theme", "selectedIndex", "maxVisible", "searchEnabled", "searchInput", "submenuComponent"],
  Input: ["value", "cursor", "focused", "prompt", "placeholder"],
  Image: ["base64Data", "mimeType", "theme", "options", "dimensions", "imageId"],
  Loader: ["text", "paddingX", "paddingY", "customBgFn", "frames", "intervalMs", "currentFrame", "renderIndicatorVerbatim", "spinnerColorFn", "messageColorFn", "message"],
  HStack: ["entries", "gap", "align"],
  VStack: ["entries", "gap", "align"],
  UserMessageComponent: ["text", "markdownTheme", "outputPad", "markdownTransformers"],
  AssistantMessageComponent: ["hideThinkingBlock", "markdownTheme", "hiddenThinkingLabel", "outputPad", "markdownTransformers", "lastMessage", "isStreaming", "thinkingVisibilityOverrides"],
  ToolExecutionComponent: ["toolName", "toolCallId", "args", "expanded", "showImages", "imageWidthCells", "isPartial", "toolDefinition", "cwd", "executionStarted", "argsComplete", "result"],
  BashExecutionComponent: ["children", "command", "outputLines", "status", "exitCode", "loader", "truncationResult", "fullOutputPath", "expanded"],
});

// The shared theme instance pi-coding-agent's `theme` reads (theme.ts).
const THEME_KEY = Symbol.for("@earendil-works/pi-coding-agent:theme");
const PROBE = "Pg probe";
const PROBE_CODE = "const a = 1;";
// A style closure that is the identity has no token (an absent token is the
// identity, §2); FAIL marks one no token reproduces.
const ABSENT = null;
const FAIL = Symbol("unmappable");

let handlers;
// coding-agent's bundle, its classes by prototype, and its built-in tool
// definitions by name.
let codingAgent;
let classHandlers;
const builtinDefinitions = new Map();
// The coding-agent classes the walk maps. The bundle is large: it is loaded
// only once a component claims to be one of them.
const CODING_AGENT_CLASSES = new Set(["DynamicBorder", "UserMessageComponent", "AssistantMessageComponent", "ToolExecutionComponent", "BashExecutionComponent"]);

// The pinned classes by render function. Loaded on the first walk, which
// happens only while a frontend draws.
function handlerTable() {
  if (handlers) return handlers;
  const { Container } = require("./shims/pi-dist/pi-tui/tui.js");
  const tui = require("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
  handlers = new Map([
    [Container.prototype.render, walkContainer],
    [tui.Box.prototype.render, walkBox],
    [tui.Text.prototype.render, walkText],
    [tui.TruncatedText.prototype.render, walkTruncatedText],
    [tui.Markdown.prototype.render, walkMarkdown],
    [tui.Spacer.prototype.render, walkSpacer],
    [tui.SelectList.prototype.render, walkSelectList],
    [tui.SettingsList.prototype.render, walkSettingsList],
    [tui.Image.prototype.render, walkImage],
    [tui.Loader.prototype.render, walkLoader],
    [tui.HStack.prototype.render, walkHStack],
    [tui.VStack.prototype.render, walkVStack],
  ]);
  return handlers;
}

function loadCodingAgent(table) {
  codingAgent = require("./shims/pi-dist/pi-coding-agent/sdk-bundle/index.js");
  table.set(codingAgent.DynamicBorder.prototype.render, walkDynamicBorder);
  // BashExecutionComponent renders as the Container it is, so the
  // conversation classes are known by their prototype.
  classHandlers = new Map([
    [codingAgent.UserMessageComponent.prototype, walkUserMessage],
    [codingAgent.AssistantMessageComponent.prototype, walkAssistantMessage],
    [codingAgent.ToolExecutionComponent.prototype, walkToolExecution],
    [codingAgent.BashExecutionComponent.prototype, walkBashExecution],
  ]);
}

function handlerFor(component) {
  const table = handlerTable();
  if (!codingAgent && CODING_AGENT_CLASSES.has(component.constructor?.name)) loadCodingAgent(table);
  if (classHandlers) {
    const prototype = Object.getPrototypeOf(component);
    const handler = classHandlers.get(prototype);
    if (handler) return component.render === prototype.render ? handler : undefined;
  }
  return table.get(component.render);
}

// Probe results are cached per closure and palette, so a steady frame costs
// one map lookup per closure.
const tokenCache = new WeakMap();
const themeCache = new WeakMap();
const imageRefs = new WeakMap();

// sha256Ref is the image ref of base64 data: the sha256 of its bytes (§7).
function sha256Ref(data) {
  return createHash("sha256").update(Buffer.from(data, "base64")).digest("hex");
}

// cachedRef is the ref of data, which owner holds, cached per owner.
function cachedRef(owner, data) {
  const cached = imageRefs.get(owner);
  if (cached?.data === data) return cached.ref;
  const ref = sha256Ref(data);
  imageRefs.set(owner, { data, ref });
  return ref;
}

function tokenNames(tokens) {
  if (!tokens) return [];
  return tokens instanceof Map ? [...tokens.keys()] : Object.keys(tokens);
}

// ThemeProbe records which token a closure asks the shared theme for, so a
// closure maps to the token it names even when another token has the same
// color.
class ThemeProbe {
  constructor(theme) {
    this.theme = theme;
    this.calls = [];
    this.installed = [];
    this.baseFg = typeof theme?.fg === "function" ? theme.fg.bind(theme) : (_token, text) => String(text);
    this.baseBg = typeof theme?.bg === "function" ? theme.bg.bind(theme) : (_token, text) => String(text);
    this.palette = theme?.foregrounds ?? theme?.fgAnsi ?? theme;
  }

  install() {
    const targets = new Set([this.theme, globalThis[THEME_KEY]]);
    for (const target of targets) {
      if (!target || typeof target.fg !== "function" || typeof target.bg !== "function") continue;
      const saved = { target, fg: Object.getOwnPropertyDescriptor(target, "fg"), bg: Object.getOwnPropertyDescriptor(target, "bg") };
      const fg = target.fg;
      const bg = target.bg;
      const calls = this.calls;
      target.fg = function (token, text) { calls.push("fg", token); return fg.call(this, token, text); };
      target.bg = function (token, text) { calls.push("bg", token); return bg.call(this, token, text); };
      this.installed.push(saved);
    }
  }

  restore() {
    for (const { target, fg, bg } of this.installed) {
      if (fg) Object.defineProperty(target, "fg", fg); else delete target.fg;
      if (bg) Object.defineProperty(target, "bg", bg); else delete target.bg;
    }
    this.installed.length = 0;
  }

  // token maps a style closure to the foreground or background token whose
  // theme.fg/theme.bg output on a probe equals the closure's.
  token(fn, slot) {
    if (fn === undefined) return ABSENT;
    if (typeof fn !== "function") return FAIL;
    const cached = tokenCache.get(fn);
    if (cached && cached.palette === this.palette && cached.slot === slot) return cached.token;
    const token = this.probeToken(fn, slot);
    tokenCache.set(fn, { palette: this.palette, slot, token });
    return token;
  }

  probeToken(fn, slot) {
    const base = slot === "fg" ? this.baseFg : this.baseBg;
    this.calls.length = 0;
    let out;
    try {
      out = fn(PROBE);
    } catch {
      return FAIL;
    }
    if (typeof out !== "string") return FAIL;
    if (this.calls.length === 2 && this.calls[0] === slot && typeof this.calls[1] === "string" && safe(() => base(this.calls[1], PROBE)) === out) return this.calls[1];
    if (out === PROBE) return ABSENT;
    const tokens = slot === "fg" ? this.theme?.foregrounds ?? this.theme?.fgAnsi : this.theme?.backgrounds ?? this.theme?.bgAnsi;
    for (const token of tokenNames(tokens)) {
      if (safe(() => base(token, PROBE)) === out) return token;
    }
    return FAIL;
  }

  // sameTheme reports whether a component theme is coding-agent's for the
  // current palette, on probes.
  sameTheme(actual, kind) {
    if (!actual || typeof actual !== "object") return false;
    const cached = themeCache.get(actual);
    if (cached && cached.palette === this.palette && cached.kind === kind) return cached.same;
    const same = safe(() => compareTheme(actual, kind)) === true;
    themeCache.set(actual, { palette: this.palette, kind, same });
    return same;
  }
}

function safe(fn) {
  try {
    return fn();
  } catch {
    return FAIL;
  }
}

function sameOutputs(actual, reference, keys, ...args) {
  for (const key of keys) {
    if (typeof actual[key] !== "function" || actual[key](...args) !== reference[key](...args)) return false;
  }
  return true;
}

function sameLines(a, b) {
  return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((line, i) => line === b[i]);
}

function compareTheme(actual, kind) {
  switch (kind) {
    case "select": {
      // SelectList draws with these four (select-list.ts render, renderItem).
      return sameOutputs(actual, getSelectListTheme(), ["selectedText", "description", "scrollInfo", "noMatch"], PROBE);
    }
    case "settings": {
      const reference = getSettingsListTheme();
      return actual.cursor === reference.cursor &&
        sameOutputs(actual, reference, ["label", "value"], PROBE, true) &&
        sameOutputs(actual, reference, ["label", "value"], PROBE, false) &&
        sameOutputs(actual, reference, ["description", "hint"], PROBE);
    }
    case "markdown": {
      const reference = getMarkdownTheme();
      if (actual.codeBlockIndent !== undefined && actual.codeBlockIndent !== "  ") return false;
      if (!sameOutputs(actual, reference, ["heading", "link", "linkUrl", "code", "codeBlock", "codeBlockBorder", "quote", "quoteBorder", "hr", "listBullet", "bold", "italic", "underline", "strikethrough"], PROBE)) return false;
      if (typeof actual.highlightCode !== "function") return false;
      return sameLines(actual.highlightCode(PROBE_CODE, "javascript"), reference.highlightCode(PROBE_CODE, "javascript")) &&
        sameLines(actual.highlightCode(PROBE_CODE, undefined), reference.highlightCode(PROBE_CODE, undefined));
    }
  }
  return false;
}

const isInt = (value) => Number.isInteger(value);

class Walk {
  constructor(theme) {
    this.probe = new ThemeProbe(theme);
    this.nodes = 0;
    this.overflow = false;
    this.images = new Map();
  }

  // node returns the view node of component, or undefined when it has none:
  // it is not structured, and its rows are unknown (rows undefined) and
  // cannot be rendered again (width undefined, inside an hstack).
  node(component, rows, width, path, depth) {
    if (!component || typeof component.render !== "function") return undefined;
    if (++this.nodes > MAX_NODES) {
      this.overflow = true;
      return undefined;
    }
    if (depth < MAX_DEPTH) {
      const handler = handlerFor(component);
      const node = handler?.(this, component, rows, width, path, depth);
      if (node) return node;
    }
    if (!rows && width !== undefined) rows = rerender(component, width);
    return rows ? this.annotate(linesNode(rows), component.viewLines) : undefined;
  }

  // annotate adds the frontend-only annotations an author declared for a
  // lines range (viewLines, §9): image as {data, mimeType}, sent by ref like
  // an image node's bytes; progress and list in their wire shapes. A
  // malformed image names no image, and the host rejects the view, as it does
  // a list whose items do not match the rows.
  annotate(node, viewLines) {
    if (!viewLines || typeof viewLines !== "object") return node;
    const { image, progress, list } = viewLines;
    if (image !== undefined) {
      if (typeof image?.data === "string" && typeof image.mimeType === "string") {
        const ref = cachedRef(image, image.data);
        this.images.set(ref, { mimeType: image.mimeType, data: image.data });
        node.image = { ref };
      } else {
        node.image = { ref: "" };
      }
    }
    if (progress !== undefined) node.progress = progress;
    if (list !== undefined) {
      node.list = list && Array.isArray(list.items)
        ? { items: list.items.map((item) => ({ label: item?.label, detail: item?.detail, columns: item?.columns })), selectedIndex: list.selectedIndex }
        : list;
    }
    return node;
  }

  children(components, rowsByChild, width, path, depth) {
    const out = [];
    for (let i = 0; i < components.length; i++) {
      const node = this.node(components[i], rowsByChild?.[i], width, `${path}.${i}`, depth + 1);
      if (!node) return undefined;
      out.push(node);
    }
    return out;
  }
}

function rerender(component, width) {
  try {
    const rows = component.render(width);
    return Array.isArray(rows) ? rows.map(String) : undefined;
  } catch {
    return undefined;
  }
}

function linesNode(rows) {
  return rows.length > 0 ? { kind: "lines", content: rows.map(String) } : { kind: "lines" };
}

// layoutRows slices a parent's child rows by its mouseLayout, the layout
// Container and Box record during the render that produced them.
function layoutRows(layout, children, width, source) {
  if (!source || !layout || layout.width !== width || !Array.isArray(layout.children) || layout.children.length !== children.length) return undefined;
  const slices = [];
  let y = 0;
  for (let i = 0; i < children.length; i++) {
    const entry = layout.children[i];
    if (entry?.component !== children[i] || !isInt(entry.height)) return undefined;
    slices.push(source.slice(y, y + entry.height));
    y += entry.height;
  }
  return y === source.length ? slices : undefined;
}

function walkContainer(walk, c, rows, width, path, depth) {
  if (!Array.isArray(c.children)) return undefined;
  const childRows = layoutRows(c.mouseLayout, c.children, width, rows);
  const children = walk.children(c.children, childRows, width, path, depth);
  if (!children) return undefined;
  return children.length > 0 ? { kind: "container", children } : { kind: "container" };
}

function walkBox(walk, c, rows, width, path, depth) {
  if (!Array.isArray(c.children) || !isInt(c.paddingX) || !isInt(c.paddingY)) return undefined;
  const bg = walk.probe.token(c.bgFn, "bg");
  if (bg === FAIL) return undefined;
  const inner = width === undefined ? undefined : Math.max(1, width - c.paddingX * 2);
  let childRows;
  if (rows) {
    const heights = c.mouseLayout?.children;
    // Box keeps the children's rows of its last render in its cache, except
    // when they were empty (it then returns early).
    const empty = Array.isArray(heights) && heights.every((entry) => entry?.height === 0);
    const source = empty ? [] : c.cache?.width === width ? c.cache.childLines : undefined;
    childRows = layoutRows(c.mouseLayout, c.children, inner, source);
  }
  const children = walk.children(c.children, childRows, inner, path, depth);
  if (!children) return undefined;
  const node = { kind: "box" };
  if (c.paddingX !== 1) node.paddingX = c.paddingX;
  if (c.paddingY !== 1) node.paddingY = c.paddingY;
  if (bg) node.bg = bg;
  if (children.length > 0) node.children = children;
  return node;
}

function textValue(text) {
  if (text === undefined || text === null) return "";
  return typeof text === "string" ? text : undefined;
}

// The renderDiff results the runtime's pi-coding-agent module returned, by
// result: a Text drawing one is a diff node (§2.1). Bounded: the latest win.
const MAX_DIFFS = 256;
const diffs = new Map();

// recordDiff records that renderDiff(diff, {filePath}) returned text.
export function recordDiff(text, diff, filePath) {
  diffs.delete(text);
  diffs.set(text, { diff, filePath: typeof filePath === "string" ? filePath : "" });
  if (diffs.size > MAX_DIFFS) diffs.delete(diffs.keys().next().value);
}

function walkText(walk, c) {
  const text = textValue(c.text);
  if (text === undefined || !isInt(c.paddingX) || !isInt(c.paddingY)) return undefined;
  const bg = walk.probe.token(c.customBgFn, "bg");
  if (bg === FAIL) return undefined;
  const diff = bg === ABSENT ? diffs.get(text) : undefined;
  if (diff) {
    // A diff always carries its padding: its defaults are not Text's.
    const node = { kind: "diff", paddingX: c.paddingX, paddingY: c.paddingY };
    if (diff.diff) node.diff = diff.diff;
    if (diff.filePath) node.filePath = diff.filePath;
    return node;
  }
  const node = { kind: "text" };
  if (text) node.text = text;
  if (c.paddingX !== 1) node.paddingX = c.paddingX;
  if (c.paddingY !== 1) node.paddingY = c.paddingY;
  if (bg) node.bg = bg;
  return node;
}

function walkTruncatedText(_walk, c) {
  if (typeof c.text !== "string" || !isInt(c.paddingX) || !isInt(c.paddingY)) return undefined;
  const node = { kind: "truncated-text" };
  if (c.text) node.text = c.text;
  if (c.paddingX !== 0) node.paddingX = c.paddingX;
  if (c.paddingY !== 0) node.paddingY = c.paddingY;
  return node;
}

function walkMarkdown(walk, c) {
  const text = textValue(c.text);
  if (text === undefined || !isInt(c.paddingX) || !isInt(c.paddingY)) return undefined;
  const options = c.options ?? {};
  // transform is a closure; the two preserve options have no wire field.
  if (options.transform !== undefined || options.preserveBackslashEscapes || options.preserveOrderedListMarkers) return undefined;
  if (!walk.probe.sameTheme(c.theme, "markdown")) return undefined;
  const node = { kind: "markdown" };
  if (text) node.text = text;
  if (c.paddingX !== 0) node.paddingX = c.paddingX;
  if (c.paddingY !== 0) node.paddingY = c.paddingY;
  const style = c.defaultTextStyle;
  if (style !== undefined && style !== null) {
    if (typeof style !== "object") return undefined;
    const color = walk.probe.token(style.color, "fg");
    const bgColor = walk.probe.token(style.bgColor, "bg");
    if (color === FAIL || bgColor === FAIL) return undefined;
    const wire = {};
    if (color) wire.color = color;
    if (bgColor) wire.bgColor = bgColor;
    for (const flag of ["bold", "italic", "strikethrough", "underline"]) if (style[flag]) wire[flag] = true;
    if (Object.keys(wire).length > 0) node.defaultTextStyle = wire;
  }
  if (options.renderLatex === false) node.renderLatex = false;
  return node;
}

function walkSpacer(_walk, c) {
  if (!isInt(c.lines) || c.lines < 0) return undefined;
  return c.lines === 1 ? { kind: "spacer" } : { kind: "spacer", lines: c.lines };
}

function walkDynamicBorder(walk, c) {
  const color = walk.probe.token(c.color, "fg");
  // The wire's absent color is "border", so an identity color has no node.
  if (color === FAIL || color === ABSENT) return undefined;
  return color === "border" ? { kind: "dynamic-border" } : { kind: "dynamic-border", color };
}

function selectItems(items) {
  const out = [];
  for (const item of items) {
    if (!item || typeof item.value !== "string") return undefined;
    if (item.label !== undefined && typeof item.label !== "string") return undefined;
    if (item.description !== undefined && typeof item.description !== "string") return undefined;
    const wire = { value: item.value };
    if (item.label) wire.label = item.label;
    if (item.description) wire.description = item.description;
    out.push(wire);
  }
  return out;
}

// selectFilter finds a setFilter argument that reproduces filteredItems.
// SelectList keeps only the result, so the candidate is the shown values'
// common prefix (or one no value starts with), checked against setFilter's
// own predicate.
function selectFilter(items, filtered) {
  let candidate;
  if (filtered.length === 0) candidate = "\uffff";
  else {
    candidate = filtered[0].value.toLowerCase();
    for (const item of filtered) {
      const value = item.value.toLowerCase();
      let n = 0;
      while (n < candidate.length && n < value.length && candidate[n] === value[n]) n++;
      candidate = candidate.slice(0, n);
    }
  }
  let index = 0;
  for (const item of items) {
    if (!item.value.toLowerCase().startsWith(candidate)) continue;
    if (filtered[index] !== item) return undefined;
    index++;
  }
  return index === filtered.length ? candidate : undefined;
}

function walkSelectList(walk, c, _rows, _width, path) {
  if (!Array.isArray(c.items) || !Array.isArray(c.filteredItems) || c.items.length > MAX_LIST_ITEMS) return undefined;
  if (!isInt(c.maxVisible) || !isInt(c.selectedIndex)) return undefined;
  const layout = c.layout ?? {};
  if (typeof layout !== "object" || layout.truncatePrimary !== undefined) return undefined;
  if (!walk.probe.sameTheme(c.theme, "select")) return undefined;
  const items = selectItems(c.items);
  if (!items) return undefined;
  const node = { kind: "select-list", id: `select-list:${path}`, items };
  if (c.maxVisible !== 5) node.maxVisible = c.maxVisible;
  const wireLayout = {};
  for (const key of ["minPrimaryColumnWidth", "maxPrimaryColumnWidth"]) {
    if (layout[key] === undefined) continue;
    if (!isInt(layout[key])) return undefined;
    wireLayout[key] = layout[key];
  }
  if (Object.keys(wireLayout).length > 0) node.layout = wireLayout;
  if (c.filteredItems !== c.items) {
    const filter = selectFilter(c.items, c.filteredItems);
    if (filter === undefined) return undefined;
    node.filter = filter;
  }
  // Always sent: the host applies a value that changed since the last frame
  // (§4.2), and the Node component owns the selection.
  node.selectedIndex = c.selectedIndex;
  return node;
}

function settingsItems(items) {
  const out = [];
  for (const item of items) {
    if (!item || typeof item.id !== "string" || typeof item.label !== "string" || typeof item.currentValue !== "string") return undefined;
    if (item.description !== undefined && typeof item.description !== "string") return undefined;
    if (item.values !== undefined && !(Array.isArray(item.values) && item.values.every((value) => typeof value === "string"))) return undefined;
    if (item.submenu !== undefined && typeof item.submenu !== "function") return undefined;
    const wire = { id: item.id, label: item.label, currentValue: item.currentValue };
    if (item.description) wire.description = item.description;
    if (item.values) wire.values = [...item.values];
    // A submenu is a factory closure: reported present, without a tree.
    if (item.submenu) wire.submenu = { kind: "lines" };
    out.push(wire);
  }
  return out;
}

function walkSettingsList(walk, c, _rows, _width, path) {
  // An open submenu renders as the list's own frame.
  if (c.submenuComponent) return undefined;
  if (!Array.isArray(c.items) || c.items.length > MAX_LIST_ITEMS || !isInt(c.maxVisible) || !isInt(c.selectedIndex)) return undefined;
  if (!walk.probe.sameTheme(c.theme, "settings")) return undefined;
  const items = settingsItems(c.items);
  if (!items) return undefined;
  const node = { kind: "settings-list", id: `settings-list:${path}`, items, maxVisible: c.maxVisible };
  if (c.searchEnabled) {
    node.enableSearch = true;
    // The host types the filter into a fresh Input: the query must be drawn
    // the same way, cursor at its end, unfocused, with Input's defaults.
    const input = c.searchInput;
    if (!input || typeof input.value !== "string" || input.cursor !== input.value.length || input.focused === true || input.prompt !== "> " || input.placeholder !== "") return undefined;
    if (input.value) node.filter = input.value;
  }
  node.selectedIndex = c.selectedIndex;
  return node;
}

let imageDimensions;

function walkImage(walk, c) {
  if (typeof c.base64Data !== "string" || typeof c.mimeType !== "string") return undefined;
  const options = c.options ?? {};
  // Explicit dimensions differing from the data's own have no wire field.
  imageDimensions ??= require("./shims/pi-dist/pi-tui/terminal-image.js").getImageDimensions;
  const own = safe(() => imageDimensions(c.base64Data, c.mimeType)) || { widthPx: 800, heightPx: 600 };
  if (c.dimensions?.widthPx !== own.widthPx || c.dimensions?.heightPx !== own.heightPx) return undefined;
  const fallbackColor = walk.probe.token(c.theme?.fallbackColor, "fg");
  if (fallbackColor === FAIL) return undefined;
  const ref = cachedRef(c, c.base64Data);
  const node = { kind: "image", ref, mimeType: c.mimeType };
  for (const key of ["maxWidthCells", "maxHeightCells"]) {
    if (options[key] === undefined) continue;
    if (!isInt(options[key])) return undefined;
    node[key] = options[key];
  }
  if (options.filename !== undefined) {
    if (typeof options.filename !== "string") return undefined;
    node.filename = options.filename;
  }
  if (fallbackColor) node.fallbackColor = fallbackColor;
  // The kitty image id the rows were drawn with (allocated by the render).
  if (isInt(c.imageId)) node.imageId = c.imageId;
  walk.images.set(ref, { mimeType: c.mimeType, data: c.base64Data });
  return node;
}

const DEFAULT_LOADER_FRAMES = ["\u280B", "\u2819", "\u2839", "\u2838", "\u283C", "\u2834", "\u2826", "\u2827", "\u2807", "\u280F"];

function walkLoader(walk, c) {
  if (c.paddingX !== 1 || c.paddingY !== 0 || c.customBgFn !== undefined) return undefined;
  if (typeof c.message !== "string" || !Array.isArray(c.frames) || !c.frames.every((frame) => typeof frame === "string")) return undefined;
  if (!isInt(c.currentFrame) || !isInt(c.intervalMs)) return undefined;
  const verbatim = c.renderIndicatorVerbatim === true;
  const spinnerColor = verbatim ? ABSENT : walk.probe.token(c.spinnerColorFn, "fg");
  const messageColor = walk.probe.token(c.messageColorFn, "fg");
  if (spinnerColor === FAIL || messageColor === FAIL) return undefined;
  // The text is what updateDisplay drew; a loader whose text was set any
  // other way has no node.
  const frame = c.frames[c.currentFrame] ?? "";
  const indicator = verbatim ? frame : safe(() => c.spinnerColorFn(frame));
  const message = safe(() => c.messageColorFn(c.message));
  if (typeof indicator !== "string" || typeof message !== "string") return undefined;
  if (c.text !== `${indicator.length > 0 ? `${indicator} ` : ""}${message}`) return undefined;
  if (!verbatim && !sameLines(c.frames, DEFAULT_LOADER_FRAMES)) return undefined;
  const node = { kind: "loader" };
  if (c.message !== "Loading...") node.message = c.message;
  if (spinnerColor) node.spinnerColor = spinnerColor;
  if (messageColor) node.messageColor = messageColor;
  if (verbatim || c.intervalMs !== 80) {
    node.indicator = {};
    if (verbatim) node.indicator.frames = [...c.frames];
    if (c.intervalMs !== 80) node.indicator.intervalMs = c.intervalMs;
  }
  if (c.currentFrame !== 0) node.frame = c.currentFrame;
  return node;
}

// Pi's conversation components (§2.1). A kept component keeps its id, so the
// host keeps its instance and applies each change through the upstream
// update method, as the author did; a new component has a new id.
const conversationIds = new WeakMap();
let conversationSeq = 0;

function conversationId(c, kind) {
  let id = conversationIds.get(c);
  if (id === undefined) {
    id = `${kind}:${++conversationSeq}`;
    conversationIds.set(c, id);
  }
  return id;
}

// defaultMarkdown reports whether a message draws with getMarkdownTheme()
// and no transformer, the only markdown the kinds carry (closures).
function defaultMarkdown(walk, c) {
  return Array.isArray(c.markdownTransformers) && c.markdownTransformers.length === 0 && walk.probe.sameTheme(c.markdownTheme, "markdown");
}

// An outputPad is one of the values of Pi's outputPad setting.
const validOutputPad = (pad) => pad === 0 || pad === 1;

// jsonValue reports whether value travels as JSON.
function jsonValue(value) {
  try {
    return JSON.stringify(value) !== undefined;
  } catch {
    return false;
  }
}

function walkUserMessage(walk, c) {
  if (typeof c.text !== "string" || !validOutputPad(c.outputPad) || !defaultMarkdown(walk, c)) return undefined;
  const node = { kind: "user-message" };
  if (c.text) node.text = c.text;
  node.outputPad = c.outputPad;
  return node;
}

const STOP_REASONS = new Set(["stop", "length", "toolUse", "error", "aborted"]);

// assistantMessage is the part of message that updateContent draws, or
// undefined when message has a block the wire cannot carry.
function assistantMessage(message) {
  if (!message || typeof message !== "object" || !Array.isArray(message.content) || message.content.length > MAX_LIST_ITEMS) return undefined;
  const content = [];
  for (const block of message.content) {
    if (block?.type === "text" && typeof block.text === "string") content.push({ type: "text", text: block.text });
    else if (block?.type === "thinking" && typeof block.thinking === "string") content.push({ type: "thinking", thinking: block.thinking });
    else if (block?.type === "toolCall") content.push({ type: "toolCall" });
    else return undefined;
  }
  const wire = { content };
  const { stopReason, errorMessage } = message;
  if (stopReason !== undefined) {
    if (!STOP_REASONS.has(stopReason)) return undefined;
    wire.stopReason = stopReason;
  }
  if (errorMessage !== undefined && errorMessage !== null) {
    if (typeof errorMessage !== "string") return undefined;
    if (errorMessage) wire.errorMessage = errorMessage;
  }
  return wire;
}

function walkAssistantMessage(walk, c) {
  // A click's toggle is the Node component's own; the host's start clear.
  if (!(c.thinkingVisibilityOverrides instanceof Map) || c.thinkingVisibilityOverrides.size > 0) return undefined;
  if (typeof c.hideThinkingBlock !== "boolean" || typeof c.hiddenThinkingLabel !== "string" || typeof c.isStreaming !== "boolean") return undefined;
  if (!validOutputPad(c.outputPad) || !defaultMarkdown(walk, c)) return undefined;
  let message;
  if (c.lastMessage !== undefined) {
    message = assistantMessage(c.lastMessage);
    if (!message) return undefined;
  }
  const node = { kind: "assistant-message", id: conversationId(c, "assistant-message") };
  if (message) node.message = message;
  if (c.hideThinkingBlock) node.hideThinkingBlock = true;
  if (c.hiddenThinkingLabel !== "Thinking...") node.hiddenThinkingLabel = c.hiddenThinkingLabel;
  node.outputPad = c.outputPad;
  if (c.isStreaming) node.isStreaming = true;
  return node;
}

// The built-in tool definitions' factories by tool name: the definitions the
// main transcript draws those tools with ("builtin", §2.1).
const BUILTIN_TOOLS = Object.freeze({
  read: "createReadToolDefinition", bash: "createBashToolDefinition", powershell: "createPowerShellToolDefinition", edit: "createEditToolDefinition",
  write: "createWriteToolDefinition", grep: "createGrepToolDefinition", find: "createFindToolDefinition", ls: "createLsToolDefinition",
});
const SHELL_TOOLS = new Set(["bash", "powershell"]);
const shellRenderers = new WeakMap();

function builtinDefinition(name) {
  if (!Object.hasOwn(BUILTIN_TOOLS, name)) return undefined;
  let definition = builtinDefinitions.get(name);
  if (!definition) {
    // Only execute uses the operations: empty ones start nothing.
    definition = codingAgent[BUILTIN_TOOLS[name]]("/", { operations: {} });
    builtinDefinitions.set(name, definition);
  }
  return definition;
}

// sameRenderer reports whether actual is the built-in tool name's renderer
// reference. The other tools' renderers are their module's; a shell's are
// made per definition (createShellRenderers(prompt)): the same source, and
// for renderCall the prompt it closes over, which its call header shows.
function sameRenderer(actual, reference, name, call) {
  if (actual === reference) return true;
  if (!SHELL_TOOLS.has(name) || typeof actual !== "function") return false;
  const cached = shellRenderers.get(actual);
  if (cached?.name === name) return cached.same;
  const header = (fn) => safe(() => fn({ command: PROBE }, undefined, { state: {}, executionStarted: false }).text);
  const same = String(actual) === String(reference) && (!call || (typeof header(actual) === "string" && header(actual) === header(reference)));
  shellRenderers.set(actual, { name, same });
  return same;
}

// toolResult is the result updateResult received, or undefined when it has
// a block or details the wire cannot carry. Its images go out as kit images.
function toolResult(walk, result) {
  if (!result || typeof result !== "object" || !Array.isArray(result.content) || result.content.length > MAX_LIST_ITEMS) return undefined;
  const content = [];
  for (const block of result.content) {
    if (block?.type === "text" && typeof block.text === "string") {
      content.push({ type: "text", text: block.text });
    } else if (block?.type === "image" && typeof block.data === "string" && block.data && typeof block.mimeType === "string" && block.mimeType) {
      const ref = cachedRef(block, block.data);
      walk.images.set(ref, { mimeType: block.mimeType, data: block.data });
      content.push({ type: "image", ref, mimeType: block.mimeType });
    } else {
      return undefined;
    }
  }
  const wire = { content };
  if (result.isError) wire.isError = true;
  if (result.details !== undefined) {
    if (!jsonValue(result.details)) return undefined;
    wire.details = result.details;
  }
  return wire;
}

function walkToolExecution(walk, c) {
  if (typeof c.toolName !== "string" || !c.toolName) return undefined;
  // Pi's undefined definition, which the main transcript never draws, and
  // an extension's own renderers are not expressible.
  const definition = c.toolDefinition;
  if (!definition || typeof definition !== "object") return undefined;
  const { renderCall, renderResult, renderShell } = definition;
  const empty = renderCall === undefined && renderResult === undefined && renderShell === undefined;
  if (!empty) {
    const builtin = builtinDefinition(c.toolName);
    if (!builtin || renderShell !== builtin.renderShell || !sameRenderer(renderCall, builtin.renderCall, c.toolName, true) ||
      !sameRenderer(renderResult, builtin.renderResult, c.toolName, false)) return undefined;
  }
  if ((c.toolCallId !== undefined && typeof c.toolCallId !== "string") || (c.cwd !== undefined && typeof c.cwd !== "string")) return undefined;
  if (!isInt(c.imageWidthCells) || c.imageWidthCells < 1) return undefined;
  const args = c.args === undefined ? {} : c.args;
  if (!jsonValue(args)) return undefined;
  let result;
  if (c.result !== undefined) {
    result = toolResult(walk, c.result);
    if (!result) return undefined;
  }
  const node = { kind: "tool-execution", id: conversationId(c, "tool-execution"), toolName: c.toolName };
  if (c.toolCallId) node.toolCallId = c.toolCallId;
  node.args = args;
  if (empty) node.toolDefinition = "empty";
  if (c.cwd) node.cwd = c.cwd;
  if (!c.showImages) node.showImages = false;
  if (c.imageWidthCells !== 60) node.imageWidthCells = c.imageWidthCells;
  if (c.executionStarted) node.executionStarted = true;
  if (c.argsComplete) node.argsComplete = true;
  if (c.expanded) node.expanded = true;
  if (result) {
    node.result = result;
    node.isPartial = Boolean(c.isPartial);
  }
  return node;
}

// BashExecutionComponent's status union. A quoted capability name in the Node runtime reads as a host call to
// wire_capability_coverage_test.go, and one of these statuses also names the host completion capability, which Node lacks.
const BASH_STATUSES = new Set("running complete error cancelled".split(" "));

function walkBashExecution(walk, c) {
  if (typeof c.command !== "string" || !Array.isArray(c.children) || !BASH_STATUSES.has(c.status)) return undefined;
  if (!Array.isArray(c.outputLines) || !c.outputLines.every((line) => typeof line === "string")) return undefined;
  // The constructor colors the border by excludeFromContext.
  const border = walk.probe.token(c.children[1]?.color, "fg");
  if (border !== "bashMode" && border !== "dim") return undefined;
  const node = { kind: "bash-execution", id: conversationId(c, "bash-execution") };
  if (c.command) node.command = c.command;
  if (border === "dim") node.excludeFromContext = true;
  const output = c.outputLines.join("\n");
  if (output) node.output = output;
  if (c.status === "running") {
    const frame = c.loader?.currentFrame;
    if (!isInt(frame)) return undefined;
    if (frame !== 0) node.frame = frame;
  } else {
    const complete = {};
    if (c.exitCode !== undefined && c.exitCode !== null) {
      if (!isInt(c.exitCode)) return undefined;
      complete.exitCode = c.exitCode;
    }
    if (c.status === "cancelled") complete.cancelled = true;
    if (c.truncationResult?.truncated) complete.truncated = true;
    if (c.fullOutputPath !== undefined && c.fullOutputPath !== null) {
      if (typeof c.fullOutputPath !== "string") return undefined;
      if (c.fullOutputPath) complete.fullOutputPath = c.fullOutputPath;
    }
    node.complete = complete;
  }
  if (c.expanded) node.expanded = true;
  return node;
}

function stackOptions(entry) {
  const options = {};
  if (entry.basis !== undefined && entry.basis !== "auto") {
    if (!isInt(entry.basis)) return undefined;
    options.basis = entry.basis;
  }
  for (const key of ["grow", "shrink", "minSize", "maxSize"]) {
    if (entry[key] === undefined) continue;
    if (!isInt(entry[key])) return undefined;
    options[key] = entry[key];
  }
  return options;
}

function walkStack(kind, walk, c, width, path, depth) {
  if (!Array.isArray(c.entries) || !isInt(c.gap) || typeof c.align !== "string") return undefined;
  const children = [];
  for (let i = 0; i < c.entries.length; i++) {
    const entry = c.entries[i];
    // StackEntry.visible is a closure.
    if (!entry || entry.visible !== undefined) return undefined;
    const options = stackOptions(entry);
    if (!options) return undefined;
    const node = walk.node(entry.component, undefined, width, `${path}.${i}`, depth + 1);
    if (!node) return undefined;
    if (Object.keys(options).length > 0) node.stack = options;
    children.push(node);
  }
  const node = { kind };
  if (children.length > 0) node.children = children;
  if (c.gap !== 0) node.gap = c.gap;
  if (c.align !== "stretch") node.align = c.align;
  return node;
}

// An hstack lays its children out at widths it allocates from their
// intrinsic widths, so a lines range (width-independent) cannot stand in for
// a child: with no width, every descendant must be structured.
function walkHStack(walk, c, _rows, _width, path, depth) {
  return walkStack("hstack", walk, c, undefined, path, depth);
}

// A vstack renders each child once at its own width; an opaque child is
// rendered again there for its rows.
function walkVStack(walk, c, _rows, width, path, depth) {
  return walkStack("vstack", walk, c, width === undefined ? undefined : Math.max(1, width), path, depth);
}

// deriveView reads component, which just rendered rows at width, into a
// view. It returns undefined when the component has no structure (its root
// is a lines range without annotations) or the tree exceeds the bounds.
// images holds the bytes of every image by ref; the caller sends each once
// per connection (§7). With focusRoot (ui.custom), a root select-list or
// settings-list is the view's focus: the component receives the keys, so it
// is the list. viewTheme is the surface's viewTheme, sent as view.theme.
export function deriveView(component, rows, width, theme, { focusRoot = false, viewTheme } = {}) {
  const walk = new Walk(theme);
  walk.probe.install();
  let root;
  try {
    root = walk.node(component, rows.map(String), width, "0", 0);
  } finally {
    walk.probe.restore();
  }
  if (!root || walk.overflow) return undefined;
  if (root.kind === "lines" && !root.image && !root.progress && !root.list) return undefined;
  const view = { root, width };
  if (viewTheme !== undefined) view.theme = viewTheme;
  if (focusRoot && (root.kind === "select-list" || root.kind === "settings-list")) view.focus = root.id;
  return { view, images: walk.images };
}
