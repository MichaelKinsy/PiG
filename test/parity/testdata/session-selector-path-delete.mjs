import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import { setImmediate } from "node:timers/promises";
import { pathToFileURL } from "node:url";

if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./internal/codingagent", "-run", "^TestUpstreamSessionSelectorPathDelete$", "-count=1", "-v"], { encoding: "utf8" });
  const records = output.split("\n").filter(line => line.startsWith("SESSION_PATH_DELETE_BASIC "));
  assert.equal(records.length, 1, "one projection from the selected Go test");
  console.log(records[0]);
} else {
  assert.equal(process.argv[2], "pi");
  const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
  assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "0.87.1", "pinned Pi package");
  const { SessionSelectorComponent } = await import(pathToFileURL(join(root, "dist/modes/interactive/components/session-selector.js")));
  const { initTheme, stopThemeWatcher } = await import(pathToFileURL(join(root, "dist/modes/interactive/theme/theme.js")));
  const { KeybindingsManager } = await import(pathToFileURL(join(root, "dist/core/keybindings.js")));
  const { setKeybindings } = await import(pathToFileURL(createRequire(join(root, "package.json")).resolve("@earendil-works/pi-tui")));
  initTheme("dark");
  setKeybindings(new KeybindingsManager());
  const session = id => ({ path: `/tmp/${id}.jsonl`, id, cwd: "", created: new Date(0), modified: new Date(0), messageCount: 1, firstMessage: "hello", allMessagesText: "hello" });
  const selector = async (sessions, current) => {
    const component = new SessionSelectorComponent(async () => sessions, async () => [], () => {}, () => {}, () => {}, () => {}, { keybindings: new KeybindingsManager() }, current);
    await setImmediate();
    return component;
  };
  const ids = component => component.getSessionList().filteredSessions.map(node => node.session.id);
  const results = [];
  for (const [query, key, confirm] of [["a", "\x1b[127;5u", false], ["a", "\x04", true], ["", "\x1b[127;5u", true]]) {
    const component = await selector([session("a"), session("b")]);
    const list = component.getSessionList(), changes = [];
    list.onDeleteConfirmationChange = path => changes.push(path);
    if (query) list.handleInput(query);
    list.handleInput(key);
    assert.deepEqual(changes, confirm ? ["/tmp/a.jsonl"] : []);
    if (!query) {
      let deleted;
      list.onDeleteSession = async path => { assert.equal(list.confirmingDeletePath, null); deleted = path; };
      list.handleInput("\r");
      assert.equal(deleted, "/tmp/a.jsonl");
      assert.deepEqual(changes, ["/tmp/a.jsonl", null]);
    }
    results.push(changes);
  }
  const base = mkdtempSync(join(process.argv[3], "selector-path-"));
  try {
    const real = join(base, "real"); mkdirSync(real);
    const aliasA = join(base, "alias-a"), aliasB = join(base, "alias-b");
    symlinkSync(real, aliasA); symlinkSync(real, aliasB);
    for (const id of ["parent", "child"]) writeFileSync(join(real, `${id}.jsonl`), id + "\n");
    const parent = { ...session("parent"), path: join(aliasB, "parent.jsonl"), name: "Parent", modified: new Date("2026-01-01") };
    const child = { ...session("child"), path: join(aliasB, "child.jsonl"), parentSessionPath: join(aliasA, "parent.jsonl"), name: "Child", modified: new Date("2025-12-31") };
    let component = await selector([parent, child]);
    const rendered = component.render(120).join("\n");
    assert.ok(rendered.includes("Parent") && rendered.includes("└─ "));
    assert.deepEqual(ids(component), ["parent", "child"]);
    results.push(ids(component));
    component = await selector([parent], join(aliasA, "parent.jsonl"));
    const list = component.getSessionList(), changes = [];
    let error;
    list.onDeleteConfirmationChange = path => changes.push(path);
    list.onError = message => { error = message; };
    list.handleInput("\x04");
    assert.deepEqual(changes, []);
    assert.equal(error, "Cannot delete the currently active session");
    results.push([changes, error]);
    const p1 = { ...session("parent-one"), name: "Parent one", modified: new Date("2026-01-02") };
    const p2 = { ...session("parent-two"), name: "Parent two", modified: new Date("2026-01-01") };
    const c2 = { ...session("child-two"), name: "Child two", parentSessionPath: p2.path, modified: new Date("2026-01-03") };
    component = await selector([p1, p2, c2]);
    assert.deepEqual(ids(component), ["parent-two", "child-two", "parent-one"]);
    results.push(ids(component));
    console.log("SESSION_PATH_DELETE_BASIC " + JSON.stringify(results));
  } finally {
    rmSync(base, { recursive: true, force: true });
    stopThemeWatcher();
  }
}
