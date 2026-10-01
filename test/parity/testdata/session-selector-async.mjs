import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "0.99.2");
const load = path => import(pathToFileURL(join(root, "dist", path)));
const { SessionSelectorComponent } = await load("modes/interactive/components/session-selector.js");
const { KeybindingsManager } = await load("core/keybindings.js");
const { setKeybindings } = await import(import.meta.resolve("@earendil-works/pi-tui", pathToFileURL(join(root, "package.json")).href));
const { initTheme } = await load("modes/interactive/theme/theme.js");
initTheme("dark");
const flush = () => new Promise(resolve => setImmediate(resolve));
const make = id => ({ path: `/tmp/${id}.jsonl`, id, cwd: "", created: new Date(0), modified: new Date(0), messageCount: 1, firstMessage: "hello", allMessagesText: "hello" });
const observed = [];
// Original session-selector-path-delete.test.ts:187 and :219. These callbacks use the real published component, not a replacement loader state machine.
for (const repeat of [false, true]) {
  setKeybindings(new KeybindingsManager());
  const current = [make("current")], all = [make("all")];
  let calls = 0, complete, loadSignal;
  const pending = new Promise(resolve => { complete = resolve; });
  const selector = new SessionSelectorComponent(async () => current, async (progress, signal) => {
    loadSignal = signal;
    calls++;
    if (repeat) progress(1, 2, all);
    return pending;
  }, () => {}, () => {}, () => {}, () => {}, { keybindings: new KeybindingsManager() });
  await flush();
  const list = selector.getSessionList();
  list.handleInput("\t");
  list.handleInput("\t");
  if (repeat) list.handleInput("\t");
  assert.equal(calls, 1);
  if (repeat) {
    assert.equal(list.getSelectedSessionPath(), all[0].path);
    assert(selector.render(120).join("\n").includes("Loading"));
  }
  complete(all);
  await flush();
  if (!repeat) {
    const output = selector.render(120).join("\n");
    assert(output.includes("Resume Session (Current Folder)"));
    assert(!output.includes("Resume Session (All)"));
  }
  observed.push([calls, list.getSelectedSessionPath()]);
  assert.equal(loadSignal.aborted, false);
  selector.handleInput("\x1b");
  assert.equal(loadSignal.aborted, false);
}
console.log("SESSION_PATH_DELETE_ASYNC " + JSON.stringify(observed));
