// SPDX-License-Identifier: MIT
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.3");
const { filterAndSortSessions } = await import(pathToFileURL(join(root, "dist/modes/interactive/components/session-selector-search.js")));
const cases = JSON.parse(readFileSync("test/parity/scenarios/session/testdata/search-cases.json", "utf8"));
for (const test of cases) {
  const sessions = test.sessions.map(s => ({ path: `/tmp/${s.id}.jsonl`, cwd: "", name: "", created: new Date(0), messageCount: 1, firstMessage: "(no messages)", ...s, modified: new Date(s.modified) }));
  const result = filterAndSortSessions(sessions, test.query, test.sortMode, test.nameFilter ?? "all").map(s => s.id);
  assert.deepEqual(result, test.want, test.name);
  console.log("SESSION_SEARCH " + JSON.stringify([test.name, result]));
}

// Drive the actual Pi selector's input handler and Enter callback, not a reimplementation of its selection policy.
const { SessionSelectorComponent } = await import(pathToFileURL(join(root, "dist/modes/interactive/components/session-selector.js")));
const { initTheme } = await import(pathToFileURL(join(root, "dist/modes/interactive/theme/theme.js")));
const { KeybindingsManager } = await import(pathToFileURL(join(root, "dist/core/keybindings.js")));
initTheme();
for (const dd1 of cases.filter(test => ["DD1 five named sessions", "fuzzy beats fixed cost"].includes(test.name))) {
  for (const initial of [0, 1, dd1.sessions.length - 1]) {
    let selected;
    let loaded;
    const ready = new Promise(resolve => { loaded = resolve; });
    const sessions = dd1.sessions.map(s => ({ path: `/tmp/${s.id}.jsonl`, cwd: "", created: new Date(0), messageCount: 1, firstMessage: "(no messages)", ...s, modified: new Date(s.modified) }));
    let component;
    component = new SessionSelectorComponent(async () => sessions, async () => sessions, path => { selected = path; }, () => {}, () => {}, () => { if (component?.getSessionList().getSelectedSessionPath()) loaded(); }, { keybindings: new KeybindingsManager() });
    await ready;
    for (let i = 0; i < initial; i++) component.handleInput("\x1b[B");
    for (const key of "delta") component.handleInput(key);
    component.handleInput("\r");
    const index = Math.min(initial, dd1.want.length - 1);
    assert.equal(selected, `/tmp/${dd1.want[index]}.jsonl`);
    console.log("SESSION_SELECTION " + JSON.stringify([dd1.name, initial, index, selected]));
  }
}

const { fuzzyMatch } = await import(import.meta.resolve("@earendil-works/pi-tui", pathToFileURL(join(root, "package.json")).href));
for (const [query, text, score] of [["é", "café", 0.30000000000000004], ["😀", "x😀", -4.7], ["ab", "a\ufeffb", -22.8], ["ab", "a\u0085b", -12.8], ["i\u0307", "İ", -124.9], ["ος", "ΟΣ", -124.9]]) {
  const result = fuzzyMatch(query, text);
  assert.deepEqual(result, { matches: true, score });
  console.log("FUZZY_UNICODE " + JSON.stringify([query, text, result.score]));
}
