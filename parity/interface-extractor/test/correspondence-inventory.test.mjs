import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { extractCorrespondenceInventory } from "../src/correspondence-inventory.mjs";

const VERSION = fs.readFileSync(new URL("../../../coding/pigversion/pigversion.go", import.meta.url), "utf8").match(/const UpstreamVersion = "([^"]+)"/)[1];

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-correspondence-"));
  const settingsPath = path.join(root, "packages/coding-agent/src/modes/interactive/components/settings-selector.ts");
  const compactionPath = path.join(root, "packages/coding-agent/src/core/compaction/compaction.ts");
  const managerPath = path.join(root, "packages/coding-agent/src/core/settings-manager.ts");
  const interactivePath = path.join(root, "packages/coding-agent/src/modes/interactive/interactive-mode.ts");
  const agentSessionPath = path.join(root, "packages/coding-agent/src/core/agent-session.ts");
  const packageCLIPath = path.join(root, "packages/coding-agent/src/package-manager-cli.ts");
  const resourceLoaderPath = path.join(root, "packages/coding-agent/src/core/resource-loader.ts");
  const utilsPath = path.join(root, "packages/coding-agent/src/core/compaction/utils.ts");
  fs.mkdirSync(path.dirname(settingsPath), { recursive: true });
  fs.mkdirSync(path.dirname(compactionPath), { recursive: true });
  fs.writeFileSync(settingsPath, `
export class SettingsSelectorComponent {
  constructor(config: any, callbacks: any) {
    const supportsImages = true;
    const items = [
      { id: "autocompact", label: "Auto", description: "Compact", currentValue: config.auto ? "true" : "false", values: ["true", "false"] },
      { id: "theme", label: "Theme", description: "Theme", currentValue: config.theme, values: THEMES.map(x => x.name) },
    ];
    if (supportsImages) {
      const autoIndex = items.findIndex((item) => item.id === "autocompact");
      items.splice(autoIndex + 1, 0, { id: "show-images", label: "Images", description: "Images", currentValue: "true", values: ["true", "false"] });
    }
    new SettingsList(items, (id: string, value: string) => {
      switch (id) {
        case "autocompact": callbacks.onAuto(value === "true"); break;
        case "show-images": callbacks.onImages(value === "true"); break;
        case "theme": callbacks.onTheme(value); break;
      }
    });
  }
}
`);
  fs.writeFileSync(compactionPath, `
const SUMMARY_PROMPT = \`summary\`;
export async function compact(signal?: AbortSignal) {
  if (ready()) await generateSummary(signal);
  await generateTurnPrefixSummary(signal);
}
async function generateTurnPrefixSummary(signal?: AbortSignal) {
  return await completeSummarization(signal);
}
`);
  fs.writeFileSync(utilsPath, "export const SYSTEM_PROMPT = `system`;\n");
  fs.writeFileSync(managerPath, `
function deepMergeSettings(base: any, overrides: any) { return merge(base, overrides); }
export class SettingsManager {
  constructor() { this.settings = deepMergeSettings(this.globalSettings, this.projectSettings); }
  setProjectTrusted(trusted: boolean) { if (trusted) this.reloadProject(); }
  async reload() { await this.writeQueue; this.load(); }
  applyOverrides(overrides: any) { this.settings = deepMergeSettings(this.settings, overrides); }
  private persistScopedSettings(scope: string) { this.storage.withLock(scope, () => this.write()); }
  private save() { this.enqueueWrite("global", () => this.persistScopedSettings("global")); }
  private saveProjectSettings(settings: any) { this.enqueueWrite("project", () => this.persistScopedSettings("project")); }
  setLastChangelogVersion(version: string) { this.save(); }
  private updateProjectSettings(field: string, update: any) { this.saveProjectSettings({}); }
}
`);
  fs.mkdirSync(path.dirname(interactivePath), { recursive: true });
  fs.writeFileSync(interactivePath, `
class InteractiveMode {
  private setupEditorSubmitHandler() { this.showSettingsSelector(); }
  private showSettingsSelector() {
    const selector = new SettingsSelectorComponent(this.settingsManager.getGlobalSettings(), {
      onAuto: (enabled: boolean) => this.session.setAutoCompactionEnabled(enabled),
      onImages: (enabled: boolean) => this.settingsManager.setShowImages(enabled),
      onTheme: (theme: string) => this.settingsManager.setTheme(theme),
    });
    this.showOverlay(selector);
  }
}
`);
  fs.writeFileSync(path.join(root, "packages/coding-agent/src/core/compaction/index.ts"), 'export * from "./compaction.ts";\n');
  fs.writeFileSync(agentSessionPath, `
import { compact } from "./compaction/index.ts";
class AgentSession {
  async _runDefaultCompaction() { return await compact(this.signal); }
  async compact() { return await this._runDefaultCompaction(); }
}
`);
  fs.writeFileSync(packageCLIPath, `
async function createCommandSettingsManager(options: any) {
  settingsManager.setProjectTrusted(options.first);
  settingsManager.setProjectTrusted(options.second);
}
`);
  fs.writeFileSync(resourceLoaderPath, `
class ResourceLoader { async loadProjectTrustExtensions() { await this.settingsManager.reload(); } }
`);
  return root;
}

test("extracts settings order, gates, values, callbacks, and static prompts", () => {
  const inventory = extractCorrespondenceInventory({ sourceRoot: fixture(), upstreamVersion: "0.84.0" });
  assert.equal(inventory.source.language, "typescript");
  const settings = inventory.tables[0];
  assert.deepEqual(settings.items.map((item) => item.id), ["autocompact", "show-images", "theme"]);
  assert.equal(settings.items[0].currentValueExpression, 'config.auto ? "true" : "false"');
  assert.deepEqual(settings.items[0].currentReads, ["config.auto"]);
  assert.deepEqual(settings.items[0].currentWrites, []);
  assert.deepEqual(settings.items[0].currentCalls, []);
  assert.equal(settings.items[1].gate, "supportsImages");
  assert.deepEqual(settings.items[1].values, ["true", "false"]);
  assert.equal(settings.items[2].valuesExpression, "THEMES.map(x => x.name)");
  assert.deepEqual(settings.callbacks, [
    { id: "autocompact", reads: ["callbacks.onAuto"], writes: [], calls: ["callbacks.onAuto"] },
    { id: "show-images", reads: ["callbacks.onImages"], writes: [], calls: ["callbacks.onImages"] },
    { id: "theme", reads: ["callbacks.onTheme"], writes: [], calls: ["callbacks.onTheme"] },
  ]);
  assert.deepEqual(settings.productionCallbacks.map((callback) => [callback.id, callback.handler]), [
    ["autocompact", "onAuto"],
    ["show-images", "onImages"],
    ["theme", "onTheme"],
  ]);
  assert.deepEqual(settings.productionCallbacks[0].segments[0].calls.map((call) => call.callee), ["this.session.setAutoCompactionEnabled"]);
  assert.deepEqual(inventory.constants.map((prompt) => [prompt.name, prompt.value, prompt.utf16Length]), [
    ["SUMMARY_PROMPT", "summary", 7],
    ["SYSTEM_PROMPT", "system", 6],
  ]);
  assert.deepEqual(inventory.functions.map((fn) => [fn.name, fn.async, fn.cancellationInputs]), [
    ["compact", true, ["signal"]],
    ["generateTurnPrefixSummary", true, ["signal"]],
    ["applyOverrides", false, []],
    ["mergeSettings", false, []],
    ["persistScopedSettings", false, []],
    ["reload", true, []],
    ["saveGlobal", false, []],
    ["saveProject", false, []],
    ["setProjectTrusted", false, []],
    ["settingsOrchestration", false, []],
  ]);
  const compact = inventory.functions.find((fn) => fn.name === "compact");
  assert.deepEqual(compact.calls.map((call) => [call.callee, call.awaited, call.conditions]), [
    ["ready", false, []],
    ["generateSummary", true, ["ready()"]],
    ["generateTurnPrefixSummary", true, []],
  ]);
  assert.deepEqual(compact.transitions, []);
  const prefix = inventory.functions.find((fn) => fn.name === "generateTurnPrefixSummary");
  assert.deepEqual(prefix.transitions.map((transition) => [transition.kind, transition.target, transition.conditions]), [
    ["return", "", []],
  ]);
  assert.match(settings.sourceHash, /^sha256:[0-9a-f]{64}$/);
  assert.match(inventory.constants[0].valueHash, /^sha256:[0-9a-f]{64}$/);
});

test("records every production callback for one setting", () => {
  const root = fixture();
  const selector = path.join(root, "packages/coding-agent/src/modes/interactive/components/settings-selector.ts");
  fs.writeFileSync(selector, fs.readFileSync(selector, "utf8").replace(
    'case "autocompact": callbacks.onAuto(value === "true"); break;',
    'case "autocompact": callbacks.onAuto(value === "true"); callbacks.onAutoSecondary(value); break;',
  ));
  const interactive = path.join(root, "packages/coding-agent/src/modes/interactive/interactive-mode.ts");
  fs.writeFileSync(interactive, fs.readFileSync(interactive, "utf8").replace(
    'onAuto: (enabled: boolean) => this.session.setAutoCompactionEnabled(enabled),',
    'onAuto: (enabled: boolean) => this.session.setAutoCompactionEnabled(enabled),\n      onAutoSecondary: (value: string) => this.footer.setAutoCompactText(value),',
  ));
  const inventory = extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.86.1" });
  const callback = inventory.tables[0].productionCallbacks.find((item) => item.id === "autocompact");
  assert.equal(callback.handler, "onAuto+onAutoSecondary");
  assert.equal(callback.segments.length, 2);
});

test("preserves a spread setting list as a reviewed expression", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/components/settings-selector.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace('values: ["true", "false"]', 'values: [...BOOLEAN_VALUES]'));
  const inventory = extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.86.1" });
  const setting = inventory.tables[0].items.find((item) => item.id === "autocompact");
  assert.deepEqual(setting.values, []);
  assert.equal(setting.valuesExpression, "[...BOOLEAN_VALUES]");
});

test("resolves prompts composed only from static constants", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/core/compaction/compaction.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace(
    "const SUMMARY_PROMPT = `summary`;",
    "const SUMMARY_SUFFIX = ` tail`;\nconst SUMMARY_PROMPT = `summary${SUMMARY_SUFFIX}`;",
  ));
  const inventory = extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.86.1" });
  const prompt = inventory.constants.find((item) => item.name === "SUMMARY_PROMPT");
  assert.equal(prompt.value, "summary tail");
  assert.equal(prompt.utf16Length, 12);
});

test("fails closed when a prompt-like constant is dynamic", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/core/compaction/compaction.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("`summary`", "makePrompt()"));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /prompt SUMMARY_PROMPT is not a static string/,
  );
});

test("fails closed when an insertion anchor is unresolved", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/components/settings-selector.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace('item.id === "autocompact"', 'item.id === "missing"'));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /insertion anchor missing is absent/,
  );
});

test("fails closed when a setting has no dispatch path", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/components/settings-selector.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace('case "show-images": callbacks.onImages(value === "true"); break;', ""));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /setting show-images has no dispatch case or submenu/,
  );
});

test("fails closed when selector dispatch loses its production callback", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/components/settings-selector.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("callbacks.onAuto(value", "callbacks.onChanged(value"));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /production callback onChanged is missing/,
  );
});

test("production callback mutation changes compiled runtime effects", () => {
  const root = fixture();
  const before = extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" });
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/interactive-mode.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("this.session.setAutoCompactionEnabled(enabled)", "this.session.setAutoCompactionEnabled(!enabled)"));
  const after = extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" });
  assert.notEqual(after.tables[0].productionCallbacks[0].segments[0].sourceHash, before.tables[0].productionCallbacks[0].segments[0].sourceHash);
});

test("fails closed when a required compaction function disappears", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/core/compaction/compaction.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("async function generateTurnPrefixSummary", "async function renamedTurnPrefixSummary"));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /compaction function extraction incomplete/,
  );
});

for (const { name, imported, callee } of [
  { name: "named barrel alias", imported: '{ compact as summarize } from "./compaction/index.ts"', callee: "summarize" },
  { name: "named direct alias", imported: '{ compact as summarize } from "./compaction/compaction.ts"', callee: "summarize" },
  { name: "namespace alias", imported: '* as summaries from "./compaction/index.ts"', callee: "summaries.compact" },
]) {
  test(`detects lower-level compact through a ${name}`, (t) => {
    const root = fixture();
    t.after(() => fs.rmSync(root, { recursive: true, force: true }));
    const relativePath = "packages/coding-agent/src/core/agent-session.ts";
    const expression = `${callee}(this.signal)`;
    fs.writeFileSync(path.join(root, relativePath), `import ${imported};\nclass AgentSession {\n  async _runDefaultCompaction() { return await ${expression}; }\n}\n`);
    const inventory = extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: VERSION });
    assert.deepEqual(inventory.functions.find((fn) => fn.name === "compact").callers, [{
      path: relativePath,
      symbol: "_runDefaultCompaction",
      expression,
      startLine: 3,
      sourceHash: `sha256:${crypto.createHash("sha256").update(expression).digest("hex")}`,
    }]);
  });
}

for (const { name, imported, parameters, body } of [
  { name: "unrelated import", imported: './other.ts', parameters: "", body: "return compact(this.signal);" },
  { name: "shadowing parameter", imported: './compaction/index.ts', parameters: "compact: (signal: unknown) => void", body: "return compact(this.signal);" },
  { name: "shadowing local", imported: './compaction/index.ts', parameters: "", body: "const compact = (signal: unknown) => signal; return compact(this.signal);" },
]) {
  test(`does not mistake a ${name} for lower-level compact`, (t) => {
    const root = fixture();
    t.after(() => fs.rmSync(root, { recursive: true, force: true }));
    const dir = path.join(root, "packages/coding-agent/src/core");
    fs.writeFileSync(path.join(dir, "other.ts"), "export function compact(signal: unknown) { return signal; }\n");
    fs.writeFileSync(path.join(dir, "agent-session.ts"), `import { compact } from "${imported}";\nclass AgentSession {\n  async _runDefaultCompaction(${parameters}) { ${body} }\n}\n`);
    assert.throws(
      () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: VERSION }),
      /agent-session.ts#_runDefaultCompaction has 0 calls to compact/,
    );
  });
}

test("fails closed when a reviewed production caller disappears", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/core/agent-session.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("await compact(", "await renamedCompact("));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /agent-session.ts#_runDefaultCompaction has 0 calls to compact/,
  );
});

test("fails closed when a required settings manager function disappears", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/core/settings-manager.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("applyOverrides(overrides", "renamedApplyOverrides(overrides"));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /settings manager function extraction incomplete/,
  );
});

test("fails closed when settings production orchestration disappears", () => {
  const root = fixture();
  const file = path.join(root, "packages/coding-agent/src/modes/interactive/interactive-mode.ts");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("showSettingsSelector()", "renamedSettingsSelector()"));
  assert.throws(
    () => extractCorrespondenceInventory({ sourceRoot: root, upstreamVersion: "0.84.0" }),
    /setupEditorSubmitHandler has 0 calls to this.showSettingsSelector/,
  );
});
