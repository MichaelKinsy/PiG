// Key parsing and matching come verbatim from the pinned @earendil-works/pi-tui
// (see pi-tui-keys.mjs), so extensions see Pi's exact keyboard semantics.
import { matchesKey } from "./pi-tui-keys.mjs";
export {
  Key,
  decodeKittyPrintable,
  decodePrintableKey,
  isKeyRelease,
  isKeyRepeat,
  isKittyProtocolActive,
  matchesKey,
  parseKey,
  setKittyProtocolActive,
} from "./pi-tui-keys.mjs";

// Width, truncation, slicing and wrapping are Pi's own (pi-tui-utils.mjs is
// the pinned dist/utils.js), so extension rows measure and fit exactly as
// under Pi, and PiG's renderer (widthx, held to the same code by
// tui/widthx/pi_width_diff_test.go) agrees with every width they compute.
import {
  getOsc8LinkAtColumn,
  sliceByColumn,
  stripTerminalSequences,
  truncateToWidth,
  visibleWidth,
  wrapTextWithAnsi,
} from "./pi-tui-utils.mjs";
export { getOsc8LinkAtColumn, sliceByColumn, stripTerminalSequences, truncateToWidth, visibleWidth, wrapTextWithAnsi };

export const CURSOR_MARKER = "\x1b_pi:c\x07";

export function fuzzyMatch(query, text) {
  query = String(query).toLowerCase();
  text = String(text).toLowerCase();
  let qi = 0;
  for (const ch of text) {
    if (qi < query.length && ch === query[qi]) qi++;
  }
  return qi === query.length;
}

export function fuzzyFilter(items, query, toText = (x) => String(x)) {
  if (!query) return items;
  return items.filter((item) => fuzzyMatch(query, toText(item)));
}

export class Component {
  invalidate() {}
  render() { return []; }
}

export class Focusable extends Component {
  constructor() {
    super();
    this.focused = false;
  }
  handleInput() {}
}

export class Container extends Focusable {
  constructor() {
    super();
    this.children = [];
  }
  addChild(child) { this.children.push(child); }
  removeChild(child) { this.children = this.children.filter((c) => c !== child); }
  render(width = 80) { return this.children.flatMap((c) => c?.render?.(width) ?? []); }
}

export class Text extends Component {
  constructor(text = "", paddingX = 1, paddingY = 1, customBgFn = undefined) {
    super();
    this.text = text;
    this.paddingX = paddingX;
    this.paddingY = paddingY;
    this.customBgFn = customBgFn;
  }
  setText(text) { this.text = text; }
  render(width = 80) {
    if (!this.text || this.text.trim() === "") return [];
    const normalized = this.text.replace(/\t/g, "   ");
    const contentWidth = Math.max(1, width - this.paddingX * 2);
    const left = " ".repeat(this.paddingX);
    const right = " ".repeat(this.paddingX);
    const content = wrapTextWithAnsi(normalized, contentWidth).map((line) => {
      const withMargins = left + line + right;
      const padded = withMargins + " ".repeat(Math.max(0, width - visibleWidth(withMargins)));
      return this.customBgFn ? this.customBgFn(padded) : padded;
    });
    const empty = " ".repeat(width);
    const padding = Array.from({ length: this.paddingY }, () => this.customBgFn ? this.customBgFn(empty) : empty);
    return [...padding, ...content, ...padding];
  }
}

export class Spacer extends Component {
  render() { return [""]; }
}

export class Markdown extends Component {
  constructor(text = "") { super(); this.text = text; }
  render(width = 80) { return wrapTextWithAnsi(this.text, width); }
}

export class Input extends Focusable {
  constructor() {
    super();
    this.value = "";
    this.onInput = undefined;
    this.onSubmit = undefined;
  }
  getValue() { return this.value; }
  setValue(value) { this.value = String(value ?? ""); }
  handleInput(data) {
    if (data === "\r") {
      this.onSubmit?.(this.value);
      return;
    }
    if (data === "\u007f") {
      this.value = this.value.slice(0, -1);
    } else if (typeof data === "string" && data.length === 1 && data >= " ") {
      this.value += data;
    }
    this.onInput?.(this.value);
  }
  render(width = 80) { return [truncateToWidth(this.value, width, "")]; }
}

export class Editor extends Input {}
export class SelectItem { constructor(label, value = label) { this.label = label; this.value = value; } }
export class SelectList extends Focusable { constructor(items = []) { super(); this.items = items; } }
export class Box extends Container {}
export class TUI {}
export class KeybindingsManager {}
export class OverlayHandle {}
export class EditorTheme {}

// Default keys for the tui.* actions components resolve through
// getKeybindings().matches(). Mirrors packages/tui/src/keybindings.ts; only the
// actions the shimmed components consult are listed, and user overrides are not
// visible to a subprocess extension.
const defaultKeybindings = new Map([
  ["tui.select.up", ["up"]],
  ["tui.select.down", ["down"]],
  ["tui.select.pageUp", ["pageUp"]],
  ["tui.select.pageDown", ["pageDown"]],
  ["tui.select.confirm", ["enter"]],
  ["tui.select.cancel", ["escape", "ctrl+c"]],
  ["tui.input.submit", ["enter"]],
  ["tui.input.tab", ["tab"]],
]);

export function getKeybindings() {
  return {
    matches(data, action) {
      const keys = defaultKeybindings.get(action);
      if (!keys) return false;
      return keys.some((key) => matchesKey(data, key));
    },
  };
}

// Flexbox-style sizing shared by the stack components. Ported from
// packages/tui/src/components/stack.ts; sizes are integer terminal columns.
function clampStackSize(size, entry) {
  const min = Math.max(0, Math.floor(entry.minSize ?? 0));
  const max = Math.max(min, Math.floor(entry.maxSize ?? Number.MAX_SAFE_INTEGER));
  return Math.max(min, Math.min(max, Math.max(0, Math.floor(size))));
}

function distributeStackSizes(sizes, entries, amount, mode) {
  let remaining = amount;
  while (remaining > 0) {
    const candidates = entries
      .map((entry, index) => ({ entry, index }))
      .filter(({ entry, index }) =>
        mode === "grow"
          ? (entry.grow ?? 0) > 0 && sizes[index] < (entry.maxSize ?? Number.MAX_SAFE_INTEGER)
          : (entry.shrink ?? 1) > 0 && sizes[index] > (entry.minSize ?? 0),
      );
    if (candidates.length === 0) return;

    const totalWeight = candidates.reduce(
      (sum, { entry, index }) =>
        sum + (mode === "grow" ? (entry.grow ?? 0) : (entry.shrink ?? 1) * Math.max(1, sizes[index])),
      0,
    );
    let distributed = 0;
    for (const { entry, index } of candidates) {
      if (remaining <= 0) break;
      const weight = mode === "grow" ? (entry.grow ?? 0) : (entry.shrink ?? 1) * Math.max(1, sizes[index]);
      const proposed = Math.max(1, Math.floor((remaining * weight) / totalWeight));
      const capacity =
        mode === "grow" ? (entry.maxSize ?? Number.MAX_SAFE_INTEGER) - sizes[index] : sizes[index] - (entry.minSize ?? 0);
      const delta = Math.min(remaining, proposed, capacity);
      if (delta <= 0) continue;
      sizes[index] = sizes[index] + (mode === "grow" ? delta : -delta);
      remaining -= delta;
      distributed += delta;
    }
    if (distributed === 0) return;
  }
}

export function visibleStackEntries(entries, viewport) {
  return entries.filter((entry) => entry.visible?.(viewport) ?? true);
}

export function allocateStackSizes(entries, intrinsicSizes, availableSize, gap) {
  const sizes = entries.map((entry, index) =>
    clampStackSize(entry.basis === undefined || entry.basis === "auto" ? (intrinsicSizes[index] ?? 0) : entry.basis, entry),
  );
  if (availableSize === undefined) return sizes;

  const contentSize = Math.max(0, Math.floor(availableSize) - Math.max(0, entries.length - 1) * gap);
  const total = sizes.reduce((sum, size) => sum + size, 0);
  if (total < contentSize) distributeStackSizes(sizes, entries, contentSize - total, "grow");
  else if (total > contentSize) distributeStackSizes(sizes, entries, total - contentSize, "shrink");
  return sizes;
}

function normalizeStackChild(child) {
  return child && typeof child === "object" && "component" in child ? child : { component: child };
}

function padToWidth(line, width) {
  const pad = Math.max(0, width - visibleWidth(line));
  return line + " ".repeat(pad);
}

export class Stack extends Component {
  constructor(children = [], options = {}) {
    super();
    this.entries = children.map(normalizeStackChild);
    this.gap = Math.max(0, Math.floor(options.gap ?? 0));
    this.align = options.align ?? "stretch";
  }
  addChild(child) { this.entries.push(normalizeStackChild(child)); }
  removeChild(component) { this.entries = this.entries.filter((entry) => entry.component !== component); }
  invalidate() { for (const entry of this.entries) entry.component?.invalidate?.(); }
}

export class HStack extends Stack {
  render(width = 80) {
    const safeWidth = Math.max(1, width);
    const entries = visibleStackEntries(this.entries, { width: safeWidth, height: Number.MAX_SAFE_INTEGER });
    if (entries.length === 0) return [];

    const intrinsicWidths = entries.map((entry) => {
      const lines = entry.component?.render?.(safeWidth) ?? [];
      return lines.reduce((max, line) => Math.max(max, visibleWidth(line)), 0);
    });
    const widths = allocateStackSizes(entries, intrinsicWidths, safeWidth, this.gap);
    const rendered = entries.map((entry, index) => (widths[index] === 0 ? [] : entry.component?.render?.(widths[index]) ?? []));
    const height = rendered.reduce((max, lines) => Math.max(max, lines.length), 0);

    // Children occupy consecutive column ranges, so each output row is the
    // children's rows padded to their allocated width and concatenated in
    // order. Upstream composites by absolute column; the sequential form is
    // equivalent for a horizontal stack.
    const result = Array.from({ length: height }, () => "");
    for (let index = 0; index < rendered.length; index++) {
      const lines = rendered[index];
      const childWidth = widths[index];
      let offset = 0;
      if (this.align === "center") offset = Math.floor((height - lines.length) / 2);
      else if (this.align === "end") offset = height - lines.length;
      const separator = index > 0 ? " ".repeat(this.gap) : "";
      for (let row = 0; row < height; row++) {
        const line = row - offset >= 0 && row - offset < lines.length ? lines[row - offset] : "";
        result[row] = result[row] + separator + padToWidth(line, childWidth);
      }
    }
    return result.map((line) => (visibleWidth(line) > safeWidth ? sliceByColumn(line, 0, safeWidth) : line));
  }
}

export class VStack extends Stack {
  render(width = 80) {
    const safeWidth = Math.max(1, width);
    const entries = visibleStackEntries(this.entries, { width: safeWidth, height: Number.MAX_SAFE_INTEGER });
    const lines = [];
    for (let index = 0; index < entries.length; index++) {
      if (index > 0) for (let g = 0; g < this.gap; g++) lines.push("");
      lines.push(...(entries[index].component?.render?.(safeWidth) ?? []));
    }
    return lines;
  }
}

// Ported from packages/tui/src/components/settings-list.ts. Submenus are
// supported because callers may supply a submenu component factory.
export class SettingsList {
  constructor(items = [], maxVisible = 10, theme = {}, onChange = () => {}, onCancel = () => {}, options = {}) {
    this.items = items;
    this.filteredItems = items;
    this.maxVisible = maxVisible;
    this.theme = {
      label: theme.label ?? ((text) => text),
      value: theme.value ?? ((text) => text),
      description: theme.description ?? ((text) => text),
      cursor: theme.cursor ?? "→ ",
      hint: theme.hint ?? ((text) => text),
    };
    this.onChange = onChange;
    this.onCancel = onCancel;
    this.searchEnabled = options.enableSearch ?? false;
    this.searchInput = this.searchEnabled ? new Input() : undefined;
    this.selectedIndex = 0;
    this.submenuComponent = null;
    this.submenuItemIndex = null;
  }

  updateValue(id, newValue) {
    const item = this.items.find((i) => i.id === id);
    if (item) item.currentValue = newValue;
  }

  invalidate() { this.submenuComponent?.invalidate?.(); }

  applyFilter(query) {
    this.filteredItems = fuzzyFilter(this.items, query, (item) => item.label);
    if (this.selectedIndex >= this.filteredItems.length) {
      this.selectedIndex = Math.max(0, this.filteredItems.length - 1);
    }
  }

  render(width = 80) {
    if (this.submenuComponent) return this.submenuComponent.render?.(width) ?? [];

    const lines = [];
    if (this.searchEnabled && this.searchInput) {
      lines.push(...this.searchInput.render(width));
      lines.push("");
    }
    if (this.items.length === 0) {
      lines.push(this.theme.hint("  No settings available"));
      return lines;
    }

    const displayItems = this.searchEnabled ? this.filteredItems : this.items;
    if (displayItems.length === 0) {
      lines.push(truncateToWidth(this.theme.hint("  No matching settings"), width));
      return lines;
    }

    const startIndex = Math.max(
      0,
      Math.min(this.selectedIndex - Math.floor(this.maxVisible / 2), displayItems.length - this.maxVisible),
    );
    const endIndex = Math.min(startIndex + this.maxVisible, displayItems.length);
    const maxLabelWidth = Math.min(30, Math.max(...this.items.map((item) => visibleWidth(item.label))));

    for (let i = startIndex; i < endIndex; i++) {
      const item = displayItems[i];
      if (!item) continue;
      const isSelected = i === this.selectedIndex;
      const prefix = isSelected ? this.theme.cursor : "  ";
      const labelPadded = item.label + " ".repeat(Math.max(0, maxLabelWidth - visibleWidth(item.label)));
      const labelText = this.theme.label(labelPadded, isSelected);
      const separator = "  ";
      const usedWidth = visibleWidth(prefix) + maxLabelWidth + visibleWidth(separator);
      const valueText = this.theme.value(truncateToWidth(item.currentValue, width - usedWidth - 2, ""), isSelected);
      lines.push(truncateToWidth(prefix + labelText + separator + valueText, width));
    }

    if (startIndex > 0 || endIndex < displayItems.length) {
      lines.push(this.theme.hint(truncateToWidth(`  (${this.selectedIndex + 1}/${displayItems.length})`, width - 2, "")));
    }

    const selectedItem = displayItems[this.selectedIndex];
    if (selectedItem?.description) {
      lines.push("");
      for (const line of wrapTextWithAnsi(selectedItem.description, width - 4)) {
        lines.push(this.theme.description(`  ${line}`));
      }
    }
    return lines;
  }

  activateItem() {
    const item = this.searchEnabled ? this.filteredItems[this.selectedIndex] : this.items[this.selectedIndex];
    if (!item) return;
    if (item.submenu) {
      this.submenuItemIndex = this.selectedIndex;
      this.submenuComponent = item.submenu(item.currentValue, (selectedValue) => {
        this.submenuComponent = null;
        this.submenuItemIndex = null;
        if (selectedValue !== undefined) {
          item.currentValue = selectedValue;
          this.onChange(item.id, selectedValue);
        }
      });
      return;
    }
    if (!item.values || item.values.length === 0) return;
    const currentIndex = item.values.indexOf(item.currentValue);
    const nextValue = item.values[(currentIndex + 1) % item.values.length];
    item.currentValue = nextValue;
    this.onChange(item.id, nextValue);
  }

  handleInput(data) {
    if (this.submenuComponent) {
      this.submenuComponent.handleInput?.(data);
      return;
    }
    const kb = getKeybindings();
    const displayItems = this.searchEnabled ? this.filteredItems : this.items;
    if (kb.matches(data, "tui.select.up")) {
      if (displayItems.length === 0) return;
      this.selectedIndex = this.selectedIndex === 0 ? displayItems.length - 1 : this.selectedIndex - 1;
    } else if (kb.matches(data, "tui.select.down")) {
      if (displayItems.length === 0) return;
      this.selectedIndex = this.selectedIndex === displayItems.length - 1 ? 0 : this.selectedIndex + 1;
    } else if (
      kb.matches(data, "tui.select.confirm") ||
      (data === " " && (!this.searchEnabled || this.searchInput?.getValue().length === 0))
    ) {
      this.activateItem();
    } else if (kb.matches(data, "tui.select.cancel")) {
      this.onCancel();
    } else if (this.searchEnabled && this.searchInput) {
      this.searchInput.handleInput(data);
      this.applyFilter(this.searchInput.getValue());
    }
  }
}

// keybindings.ts (Pi 0.87.1, verbatim table)
export const TUI_KEYBINDINGS = {
	"tui.editor.cursorUp": { defaultKeys: "up", description: "Move cursor up" },
	"tui.editor.cursorDown": { defaultKeys: "down", description: "Move cursor down" },
	"tui.editor.historyPrevious": {
		defaultKeys: [],
		description: "Select previous prompt history entry",
	},
	"tui.editor.historyNext": {
		defaultKeys: [],
		description: "Select next prompt history entry",
	},
	"tui.editor.cursorLeft": {
		defaultKeys: ["left", "ctrl+b"],
		description: "Move cursor left",
	},
	"tui.editor.cursorRight": {
		defaultKeys: ["right", "ctrl+f"],
		description: "Move cursor right",
	},
	"tui.editor.cursorWordLeft": {
		defaultKeys: ["alt+left", "ctrl+left", "alt+b"],
		description: "Move cursor word left",
	},
	"tui.editor.cursorWordRight": {
		defaultKeys: ["alt+right", "ctrl+right", "alt+f"],
		description: "Move cursor word right",
	},
	"tui.editor.cursorLineStart": {
		defaultKeys: ["home", "ctrl+home", "ctrl+a"],
		description: "Move to line start",
	},
	"tui.editor.cursorLineEnd": {
		defaultKeys: ["end", "ctrl+end", "ctrl+e"],
		description: "Move to line end",
	},
	"tui.editor.jumpForward": {
		defaultKeys: "ctrl+]",
		description: "Jump forward to character",
	},
	"tui.editor.jumpBackward": {
		defaultKeys: "ctrl+alt+]",
		description: "Jump backward to character",
	},
	"tui.editor.pageUp": { defaultKeys: ["pageUp", "ctrl+pageUp"], description: "Page up" },
	"tui.editor.pageDown": { defaultKeys: ["pageDown", "ctrl+pageDown"], description: "Page down" },
	"tui.editor.deleteCharBackward": {
		defaultKeys: "backspace",
		description: "Delete character backward",
	},
	"tui.editor.deleteCharForward": {
		defaultKeys: ["delete", "ctrl+d"],
		description: "Delete character forward",
	},
	"tui.editor.deleteWordBackward": {
		defaultKeys: ["ctrl+w", "alt+backspace"],
		description: "Delete word backward",
	},
	"tui.editor.deleteWordForward": {
		defaultKeys: ["alt+d", "alt+delete"],
		description: "Delete word forward",
	},
	"tui.editor.deleteToLineStart": {
		defaultKeys: "ctrl+u",
		description: "Delete to line start",
	},
	"tui.editor.deleteToLineEnd": {
		defaultKeys: "ctrl+k",
		description: "Delete to line end",
	},
	"tui.editor.yank": { defaultKeys: "ctrl+y", description: "Yank" },
	"tui.editor.yankPop": { defaultKeys: "alt+y", description: "Yank pop" },
	"tui.editor.undo": { defaultKeys: "ctrl+-", description: "Undo" },
	"tui.input.newLine": { defaultKeys: ["shift+enter", "ctrl+j"], description: "Insert newline" },
	"tui.input.submit": { defaultKeys: "enter", description: "Submit input" },
	"tui.input.tab": { defaultKeys: "tab", description: "Tab / autocomplete" },
	"tui.input.copy": { defaultKeys: "ctrl+c", description: "Copy selection" },
	"tui.select.up": { defaultKeys: "up", description: "Move selection up" },
	"tui.select.down": { defaultKeys: "down", description: "Move selection down" },
	"tui.select.pageUp": { defaultKeys: "pageUp", description: "Selection page up" },
	"tui.select.pageDown": {
		defaultKeys: "pageDown",
		description: "Selection page down",
	},
	"tui.select.confirm": { defaultKeys: "enter", description: "Confirm selection" },
	"tui.select.cancel": {
		defaultKeys: ["escape", "ctrl+c"],
		description: "Cancel selection",
	},
	// These intentionally shadow the unmodified editor bindings in fullscreen mode.
	"tui.altScreen.pageUp": {
		defaultKeys: "pageUp",
		description: "Scroll viewport up one page",
	},
	"tui.altScreen.pageDown": {
		defaultKeys: "pageDown",
		description: "Scroll viewport down one page",
	},
	"tui.altScreen.halfPageUp": {
		defaultKeys: [],
		description: "Scroll viewport up half a page",
	},
	"tui.altScreen.halfPageDown": {
		defaultKeys: [],
		description: "Scroll viewport down half a page",
	},
	"tui.altScreen.lineUp": {
		defaultKeys: [],
		description: "Scroll viewport up one line",
	},
	"tui.altScreen.lineDown": {
		defaultKeys: [],
		description: "Scroll viewport down one line",
	},
	"tui.altScreen.previousPrompt": {
		defaultKeys: ["ctrl+shift+up", "ctrl+up"],
		description: "Jump to previous semantic prompt",
	},
	"tui.altScreen.nextPrompt": {
		defaultKeys: ["ctrl+shift+down", "ctrl+down"],
		description: "Jump to next semantic prompt",
	},
	"tui.altScreen.search": {
		defaultKeys: "ctrl+shift+f",
		description: "Search the primary scroll view",
	},
	"tui.altScreen.searchNext": {
		defaultKeys: ["enter", "ctrl+g"],
		description: "Select the next search match",
	},
	"tui.altScreen.searchPrevious": {
		defaultKeys: ["shift+enter", "ctrl+shift+g"],
		description: "Select the previous search match",
	},
	"tui.altScreen.searchClose": {
		defaultKeys: "escape",
		description: "Close transcript search",
	},
	"tui.altScreen.top": { defaultKeys: "home", description: "Scroll viewport to top" },
	"tui.altScreen.bottom": { defaultKeys: "end", description: "Scroll viewport to bottom" },
};

// tui.ts
export function isFocusable(component) {
  return component !== null && "focused" in component;
}

// terminal-image.ts
export function hyperlink(text, url) {
  return `\x1b]8;;${url}\x1b\\${text}\x1b]8;;\x1b\\`;
}

// components/truncated-text.ts
export class TruncatedText {
  constructor(text, paddingX = 0, paddingY = 0) {
    this.text = text;
    this.paddingX = paddingX;
    this.paddingY = paddingY;
  }
  invalidate() {}
  render(width) {
    const result = [];
    const emptyLine = " ".repeat(width);
    for (let i = 0; i < this.paddingY; i++) result.push(emptyLine);
    const availableWidth = Math.max(1, width - this.paddingX * 2);
    let singleLineText = this.text;
    const newlineIndex = this.text.indexOf("\n");
    if (newlineIndex !== -1) singleLineText = this.text.substring(0, newlineIndex);
    const displayText = truncateToWidth(singleLineText, availableWidth);
    const lineWithPadding = " ".repeat(this.paddingX) + displayText + " ".repeat(this.paddingX);
    result.push(lineWithPadding + " ".repeat(Math.max(0, width - visibleWidth(lineWithPadding))));
    for (let i = 0; i < this.paddingY; i++) result.push(emptyLine);
    return result;
  }
}

// components/loader.ts
const DEFAULT_LOADER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
const DEFAULT_LOADER_INTERVAL_MS = 80;

export class Loader extends Text {
  constructor(ui, spinnerColorFn, messageColorFn, message = "Loading...", indicator) {
    super("", 1, 0);
    this.frames = [...DEFAULT_LOADER_FRAMES];
    this.intervalMs = DEFAULT_LOADER_INTERVAL_MS;
    this.currentFrame = 0;
    this.intervalId = null;
    this.ui = ui;
    this.renderIndicatorVerbatim = false;
    this.spinnerColorFn = spinnerColorFn;
    this.messageColorFn = messageColorFn;
    this.message = message;
    this.setIndicator(indicator);
  }
  render(width) { return ["", ...super.render(width)]; }
  start() { this.updateDisplay(); this.restartAnimation(); }
  stop() {
    if (this.intervalId) {
      clearInterval(this.intervalId);
      this.intervalId = null;
    }
  }
  setMessage(message) { this.message = message; this.updateDisplay(); }
  invalidate() { super.invalidate?.(); this.updateDisplay(); }
  setIndicator(indicator) {
    this.renderIndicatorVerbatim = indicator !== undefined;
    this.frames = indicator?.frames !== undefined ? [...indicator.frames] : [...DEFAULT_LOADER_FRAMES];
    this.intervalMs = indicator?.intervalMs && indicator.intervalMs > 0 ? indicator.intervalMs : DEFAULT_LOADER_INTERVAL_MS;
    this.currentFrame = 0;
    this.start();
  }
  restartAnimation() {
    this.stop();
    if (this.frames.length <= 1) return;
    this.intervalId = setInterval(() => {
      this.currentFrame = (this.currentFrame + 1) % this.frames.length;
      this.updateDisplay();
    }, this.intervalMs);
  }
  getRenderedIndicator() {
    const frame = this.frames[this.currentFrame] ?? "";
    return this.renderIndicatorVerbatim ? frame : this.spinnerColorFn(frame);
  }
  updateDisplay() {
    const renderedFrame = this.getRenderedIndicator();
    const indicator = renderedFrame.length > 0 ? `${renderedFrame} ` : "";
    this.setText(`${indicator}${this.messageColorFn(this.message)}`);
    if (this.ui) this.ui.requestRender?.();
  }
}

// components/cancellable-loader.ts. The host owns keybinding overrides, so the
// shim matches tui.select.cancel's default keys.
export class CancellableLoader extends Loader {
  constructor(...args) {
    super(...args);
    this.abortController = new AbortController();
    this.onAbort = undefined;
  }
  get signal() { return this.abortController.signal; }
  get aborted() { return this.abortController.signal.aborted; }
  handleInput(data) {
    const keys = [].concat(TUI_KEYBINDINGS["tui.select.cancel"].defaultKeys);
    if (keys.some((key) => matchesKey(data, key))) {
      this.abortController.abort();
      this.onAbort?.();
    }
  }
  dispose() { this.stop(); }
}

// pig divergence (D73): terminal images, capability probing, screens and the
// renderer itself belong to PiG's Go host. They are importable here and throw
// a descriptive error only when called or constructed.
function hostOnlyTui(name) {
  return new Error(`${name} is not available to extensions running in PiG: the terminal is owned by PiG's host (see DIVERGENCES.md D73)`);
}
function hostOnlyTuiFunction(name) {
  const fn = function () { throw hostOnlyTui(name); };
  Object.defineProperty(fn, "name", { value: name });
  return fn;
}
function hostOnlyTuiClass(name) {
  return { [name]: class { constructor() { throw hostOnlyTui(name); } } }[name];
}
export const CombinedAutocompleteProvider = hostOnlyTuiClass("CombinedAutocompleteProvider");
export const Image = hostOnlyTuiClass("Image");
export const Marked = hostOnlyTuiClass("Marked");
export const MouseRegion = hostOnlyTuiClass("MouseRegion");
export const ProcessTerminal = hostOnlyTuiClass("ProcessTerminal");
export const ScrollView = hostOnlyTuiClass("ScrollView");
export const StdinBuffer = hostOnlyTuiClass("StdinBuffer");
export const TuiAltScreen = hostOnlyTuiClass("TuiAltScreen");
export const TuiMainScreen = hostOnlyTuiClass("TuiMainScreen");
export const allocateImageId = hostOnlyTuiFunction("allocateImageId");
export const calculateImageRows = hostOnlyTuiFunction("calculateImageRows");
export const compositeTuiLine = hostOnlyTuiFunction("compositeTuiLine");
export const deleteAllKittyImages = hostOnlyTuiFunction("deleteAllKittyImages");
export const deleteKittyImage = hostOnlyTuiFunction("deleteKittyImage");
export const detectCapabilities = hostOnlyTuiFunction("detectCapabilities");
export const encodeITerm2 = hostOnlyTuiFunction("encodeITerm2");
export const encodeKitty = hostOnlyTuiFunction("encodeKitty");
export const getCapabilities = hostOnlyTuiFunction("getCapabilities");
export const getCellDimensions = hostOnlyTuiFunction("getCellDimensions");
export const getGifDimensions = hostOnlyTuiFunction("getGifDimensions");
export const getImageDimensions = hostOnlyTuiFunction("getImageDimensions");
export const getJpegDimensions = hostOnlyTuiFunction("getJpegDimensions");
export const getNativeClipboard = hostOnlyTuiFunction("getNativeClipboard");
export const getPngDimensions = hostOnlyTuiFunction("getPngDimensions");
export const getWebpDimensions = hostOnlyTuiFunction("getWebpDimensions");
export const imageFallback = hostOnlyTuiFunction("imageFallback");
export const isViewportTUI = hostOnlyTuiFunction("isViewportTUI");
export const parseOsc11BackgroundColor = hostOnlyTuiFunction("parseOsc11BackgroundColor");
export const parseTerminalColorSchemeReport = hostOnlyTuiFunction("parseTerminalColorSchemeReport");
export const renderImage = hostOnlyTuiFunction("renderImage");
export const renderLatex = hostOnlyTuiFunction("renderLatex");
export const resetCapabilitiesCache = hostOnlyTuiFunction("resetCapabilitiesCache");
export const setCapabilities = hostOnlyTuiFunction("setCapabilities");
export const setCapabilityOverrides = hostOnlyTuiFunction("setCapabilityOverrides");
export const setCellDimensions = hostOnlyTuiFunction("setCellDimensions");
export const setKeybindings = hostOnlyTuiFunction("setKeybindings");
