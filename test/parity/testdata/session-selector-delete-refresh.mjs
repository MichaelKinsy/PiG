import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "0.87.1");
const load = path => import(pathToFileURL(join(root, "dist", path)));
const { SessionSelectorComponent } = await load("modes/interactive/components/session-selector.js");
const { KeybindingsManager } = await load("core/keybindings.js");
const { setKeybindings } = await import(import.meta.resolve("@earendil-works/pi-tui", pathToFileURL(join(root, "package.json")).href));
const { initTheme } = await load("modes/interactive/theme/theme.js");
initTheme("dark");
const flush = () => new Promise(resolve => setImmediate(resolve));
const directory = mkdtempSync(join(tmpdir(), "session-delete-refresh-"));
const bin = join(directory, "bin");
mkdirSync(bin);
// Own every deletion; do not depend on a host trash installation. Windows can use Pi's unlink fallback.
writeFileSync(join(bin, "trash"), '#!/usr/bin/env node\nrequire("node:fs").unlinkSync(process.argv.at(-1));\n', {mode:0o700});
process.env.PATH = bin + (process.platform === "win32" ? ";" : ":") + process.env.PATH;
const observed = [];
try {
  for (const tc of [
    {name:"current", scope:"current"},
    {name:"all", scope:"all"},
    {name:"current singleton", scope:"current", only:true},
    {name:"all singleton", scope:"all", only:true},
    {name:"filtered singleton", scope:"current", filtered:true},
    {name:"touched selection", scope:"current", move:true},
    {name:"deleted parent", scope:"current", child:true},
  ]) {
    setKeybindings(new KeybindingsManager());
    const home = mkdtempSync(join(directory, "case-"));
    const make = (id, name) => {
      const path = join(home, id + ".jsonl");
      writeFileSync(path, "{}\n");
      return {path,id,name,cwd:"",created:new Date(0),modified:new Date(0),messageCount:1,firstMessage:"hello",allMessagesText:"hello"};
    };
    const victim = make("deleted", "delete-target");
    const currentKeep = make("current-keep", "current-survivor");
    const allKeep = make("all-keep", "all-survivor");
    if (tc.child) currentKeep.parentSessionPath = victim.path;
    const current = tc.only ? [victim] : [victim,currentKeep];
    const all = tc.only ? [victim] : [victim,allKeep];
    if (tc.move) current.push(make("other", "other-survivor"));
    let mutated = false, refreshes = 0, finish, enterRefresh;
    const pending = new Promise(resolve => { finish = resolve; });
    const entered = new Promise(resolve => { enterRefresh = resolve; });
    const loader = rows => async () => {
      if (!mutated) return rows;
      refreshes++;
      enterRefresh();
      return pending;
    };
    let selected = "";
    const selector = new SessionSelectorComponent(loader(current),loader(all),path => {selected=basename(path,".jsonl");},()=>{},()=>{},()=>{}, {keybindings:new KeybindingsManager()});
    const list = selector.getSessionList();
    await flush();
    list.handleInput("\t");
    await flush();
    if (tc.scope === "current") list.handleInput("\t");
    if (tc.filtered) list.handleInput("delete");
    const originalDelete = list.onDeleteSession;
    let deletion;
    list.onDeleteSession = path => {mutated=true; deletion=originalDelete(path); return deletion;};
    list.handleInput("\x04");
    list.handleInput("\r");
    if (tc.move) list.handleInput("\x1b[B");
    await entered;
    assert.equal(refreshes,1);
    const rows = list.filteredSessions.map(node=>node.session.id);
    const wanted = tc.only || tc.filtered ? [] : [tc.scope === "all" ? "all-keep" : "current-keep", ...(tc.move ? ["other"] : [])];
    assert.deepEqual(rows,wanted);
    assert(!selector.render(120).join("\n").includes(victim.name));
    assert.equal(current[0].id,victim.id);
    assert.equal(all[0].id,victim.id);
    if (tc.child) assert.equal(list.filteredSessions[0].depth,0);
    list.handleInput("\r");
    assert.equal(selected,wanted[0] ?? "");
    observed.push([tc.name,rows,selected]);
    if (!selected) list.handleInput("\x1b");
    finish((tc.scope === "all" ? all : current).filter(session=>session.path!==victim.path));
    await deletion;
  }
  console.log("SESSION_DELETE_REFRESH " + JSON.stringify(observed));
} finally {
  rmSync(directory,{recursive:true,force:true});
}
