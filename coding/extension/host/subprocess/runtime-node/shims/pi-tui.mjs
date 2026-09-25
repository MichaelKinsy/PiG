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
