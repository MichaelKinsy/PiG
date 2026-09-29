import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { extractBehaviorInputInventory } from "../src/behavior-input-inventory.mjs";

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-behavior-inputs-"));
  const file = path.join(root, "packages/coding-agent/src/components/picker.ts");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `
interface Keybindings { "tui.select.up": true }
const TUI_KEYBINDINGS = {
  "tui.select.up": { defaultKeys: "up", description: "Move selection up" },
} as const;
const PRIMARY_WIDTH = 32;
class Picker {
  private index = 0;
  public onSelect?: () => void;
  handleInput(data: string): void {
    if (kb.matches(data, "tui.select.up")) {
      this.index = (this.index - 1 + 3) % 3;
    } else if (matchesKey(data, Key.escape)) {
      this.onSelect?.();
    } else if (data === " ") {
      this.input.handleInput(data);
    }
  }
  render(width: number): string[] {
    const prefix = "→ ";
    return [theme.fg("accent", prefix.repeat(Math.max(2, Math.min(width, PRIMARY_WIDTH))))];
  }
}
`);
  return { root, file };
}

test("extracts stable input-handler branches, effects, and source identity", () => {
  const f = fixture();
  const inventory = extractBehaviorInputInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" });
  assert.equal(inventory.handlers.length, 1);
  assert.deepEqual(inventory.keybindings, [{
    id: "tui.select.up",
    defaults: { darwin: ["up"], linux: ["up"], linuxWsl: ["up"], win32: ["up"] },
    description: "Move selection up",
    path: "packages/coding-agent/src/components/picker.ts",
    line: 4,
    consumers: ["input:packages/coding-agent/src/components/picker.ts#Picker.handleInput"],
  }]);
  const handler = inventory.handlers[0];
  assert.equal(handler.id, "input:packages/coding-agent/src/components/picker.ts#Picker.handleInput");
  assert.deepEqual(handler.bindings, ["matchesKey:Key.escape", "tui.select.up"]);
  assert.deepEqual(handler.rawInputs, ['" "']);
  assert.deepEqual(handler.callbacks, ["this.onSelect"]);
  assert.deepEqual(handler.delegates, ["this.input.handleInput"]);
  assert.deepEqual(handler.mutations, ["this.index"]);
  assert.deepEqual(handler.boundaryOperators, ["%"]);
  assert.equal(handler.branchCount, 3);
  assert.match(handler.sourceHash, /^sha256:[0-9a-f]{64}$/);
  assert.equal(inventory.renderers.length, 1);
  const renderer = inventory.renderers[0];
  assert.deepEqual(renderer.themeCalls, ['theme.fg("accent")']);
  assert.deepEqual(renderer.layoutCalls, ["Math.max", "Math.min", "prefix.repeat"]);
  assert.deepEqual(renderer.glyphs, ['"→ "']);
  assert.deepEqual(renderer.numericLiterals, ["2"]);
  assert.deepEqual(renderer.dependencies, [{ name: "PRIMARY_WIDTH", value: "32", line: 6 }]);
});

test("extracts WSL keybinding defaults separately from ordinary Linux", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-wsl-keybindings-"));
  const file = path.join(root, "packages/coding-agent/src/core/keybindings.ts");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `
interface AppKeybindings { "app.model.previous": true }
const windowsKeybindings = useWindowsKeybindings();
const KEYBINDINGS = {
  "app.model.previous": { defaultKeys: windowsKeybindings ? "alt+p" : "shift+ctrl+p", description: "Previous model" },
} as const;
class Handler { handleInput(data: string) { if (kb.matches(data, "app.model.previous")) this.previous(); } }
`);
  const inventory = extractBehaviorInputInventory({ sourceRoot: root, upstreamVersion: "0.86.1" });
  assert.deepEqual(inventory.keybindings[0].defaults, {
    darwin: ["shift+ctrl+p"],
    linux: ["shift+ctrl+p"],
    linuxWsl: ["alt+p"],
    win32: ["alt+p"],
  });
});

test("inherits keybinding descriptions through TUI definition spreads", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-spread-keybindings-"));
  const file = path.join(root, "packages/coding-agent/src/core/keybindings.ts");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `
interface Keybindings { "tui.altScreen.nextPrompt": true }
const TUI_KEYBINDINGS = {
  "tui.altScreen.nextPrompt": { defaultKeys: "ctrl+down", description: "Jump to next prompt" },
} as const;
const KEYBINDINGS = {
  ...TUI_KEYBINDINGS,
  "tui.altScreen.nextPrompt": {
    ...TUI_KEYBINDINGS["tui.altScreen.nextPrompt"],
    defaultKeys: ["ctrl+shift+down", "ctrl+down"],
  },
} as const;
class Handler { handleInput(data: string) { if (kb.matches(data, "tui.altScreen.nextPrompt")) this.next(); } }
`);
  const inventory = extractBehaviorInputInventory({ sourceRoot: root, upstreamVersion: "0.86.1" });
  assert.equal(inventory.keybindings[0].description, "Jump to next prompt");
  assert.deepEqual(inventory.keybindings[0].defaults.linux, ["ctrl+shift+down", "ctrl+down"]);
});

test("fills spread descriptions when the TUI base file is visited after the coding override", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-cross-package-keybindings-"));
  const codingFile = path.join(root, "packages/coding-agent/src/core/keybindings.ts");
  const tuiFile = path.join(root, "packages/tui/src/keybindings.ts");
  fs.mkdirSync(path.dirname(codingFile), { recursive: true });
  fs.mkdirSync(path.dirname(tuiFile), { recursive: true });
  fs.writeFileSync(codingFile, `
interface Keybindings { "tui.altScreen.nextPrompt": true }
const KEYBINDINGS = {
  "tui.altScreen.nextPrompt": {
    ...TUI_KEYBINDINGS["tui.altScreen.nextPrompt"],
    defaultKeys: ["ctrl+shift+down", "ctrl+down"],
  },
} as const;
class Handler { handleInput(data: string) { if (kb.matches(data, "tui.altScreen.nextPrompt")) this.next(); } }
`);
  fs.writeFileSync(tuiFile, `
const TUI_KEYBINDINGS = {
  "tui.altScreen.nextPrompt": { defaultKeys: "ctrl+down", description: "Jump to next prompt" },
} as const;
`);
  const inventory = extractBehaviorInputInventory({ sourceRoot: root, upstreamVersion: "0.86.1" });
  assert.equal(inventory.keybindings[0].description, "Jump to next prompt");
  assert.deepEqual(inventory.keybindings[0].defaults.linux, ["ctrl+shift+down", "ctrl+down"]);
  assert.equal(inventory.keybindings[0].path, "packages/coding-agent/src/core/keybindings.ts");
});

test("source and event changes alter the generated contract denominator", () => {
  const f = fixture();
  const before = extractBehaviorInputInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }).handlers[0];
  fs.writeFileSync(f.file, fs.readFileSync(f.file, "utf8").replaceAll("tui.select.up", "tui.select.down"));
  const after = extractBehaviorInputInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }).handlers[0];
  assert.notEqual(after.sourceHash, before.sourceHash);
  assert.deepEqual(after.bindings, ["matchesKey:Key.escape", "tui.select.down"]);
});

test("distinguishes object render delegates from their enclosing class renderer", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-nested-renderers-"));
  const file = path.join(root, "packages/tui/src/tui-alt-screen.ts");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `
class TuiAltScreen {
  implicitDocument: { render(width: number): string[] };
  constructor() {
    this.implicitDocument = { render: (width) => this.render(width) };
  }
  render(width: number): string[] { return [String(width)]; }
}
`);
  const inventory = extractBehaviorInputInventory({ sourceRoot: root, upstreamVersion: "0.84.0" });
  assert.deepEqual(inventory.renderers.map((renderer) => renderer.id), [
    "render:packages/tui/src/tui-alt-screen.ts#TuiAltScreen.implicitDocument.render",
    "render:packages/tui/src/tui-alt-screen.ts#TuiAltScreen.render",
  ]);
});

test("distinguishes module object renderers passed through factories", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-factory-renderers-"));
  const file = path.join(root, "packages/agent/src/system.ts");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `
const sections = {
  identity: defineSection({ render: (value) => value }),
  cwd: defineSection({ render: (value) => value.cwd }),
};
`);
  const inventory = extractBehaviorInputInventory({ sourceRoot: root, upstreamVersion: "0.86.1" });
  assert.deepEqual(inventory.renderers.map((renderer) => renderer.id), [
    "render:packages/agent/src/system.ts#<module>.cwd.render",
    "render:packages/agent/src/system.ts#<module>.identity.render",
  ]);
});

test("ac47_production_reachable_private_dialog_logic", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-private-dialog-inputs-"));
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/interactive-mode.ts");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `
class InteractiveMode {
  private extensionSelector?: object;
  private showExtensionSelector(): Promise<string | undefined> {
    return new Promise((resolve) => {
      this.extensionSelector = createSelector(
        (value) => { this.hideExtensionSelector(); resolve(value); },
        () => { this.hideExtensionSelector(); resolve(undefined); },
      );
    });
  }
}
`);
  const inventory = extractBehaviorInputInventory({ sourceRoot: root, upstreamVersion: "0.83.0" });
  assert.equal(inventory.handlers.length, 1);
  assert.equal(inventory.handlers[0].id, "input:packages/coding-agent/src/modes/interactive/interactive-mode.ts#InteractiveMode.showExtensionSelector");
  assert.deepEqual(inventory.handlers[0].mutations, ["this.extensionSelector"]);
  assert.deepEqual(inventory.handlers[0].callbacks, ["this.hideExtensionSelector"]);
});

test("tracks production-reachable private queue control flow", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-private-queue-inputs-"));
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/interactive-mode.ts");
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `
class InteractiveMode {
  private compactionQueuedMessages: string[] = [];
  private async flushCompactionQueue(willRetry: boolean): Promise<void> {
    if (this.compactionQueuedMessages.length === 0) return;
    const queued = [...this.compactionQueuedMessages];
    this.compactionQueuedMessages = [];
    if (willRetry) {
      for (const message of queued) await this.session.steer(message);
    }
  }
}
`);
  const inventory = extractBehaviorInputInventory({ sourceRoot: root, upstreamVersion: "0.83.0" });
  assert.equal(inventory.handlers.length, 1);
  assert.equal(inventory.handlers[0].id, "input:packages/coding-agent/src/modes/interactive/interactive-mode.ts#InteractiveMode.flushCompactionQueue");
  assert.equal(inventory.handlers[0].async, true);
  assert.equal(inventory.handlers[0].branchCount, 2);
  assert.deepEqual(inventory.handlers[0].mutations, ["this.compactionQueuedMessages"]);
});

test("renderer dependencies catch positioning changes outside the render method", () => {
  const f = fixture();
  const before = extractBehaviorInputInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }).renderers[0];
  fs.writeFileSync(f.file, fs.readFileSync(f.file, "utf8").replace("PRIMARY_WIDTH = 32", "PRIMARY_WIDTH = 40"));
  const after = extractBehaviorInputInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }).renderers[0];
  assert.equal(after.sourceHash, before.sourceHash);
  assert.notDeepEqual(after.dependencies, before.dependencies);
  assert.equal(after.dependencies[0].value, "40");
});
