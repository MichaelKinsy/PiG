// Drive the installed pi-tui KeybindingsManager (the tui.* table) with seeded user keybinding configs: resolved bindings, definitions, keys, conflicts, user bindings, and which input matches which keybinding, before and after setUserBindings.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { KeybindingsManager, TUI_KEYBINDINGS } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const inputs = ["\x1b", "\x03", "\r", "\n", "\t", "\x1b[Z", "\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x7f", "\x1b[3~", "\x01", "\x05", "\x0b", "\x15", "\x17", "\x19", "\x1a", "\x1f", "\x1bb", "\x1bf", "\x1bd", "\x1b[1;5C", "\x1b[1;5D", "\x1b[5~", "\x1b[6~", "\x1b[H", "\x1b[F", "a", " ", "\x0c", "\x0e", "\x10", "\x12", "\x13", "\x06", "\x04"];
const snapshot = (manager) => {
  const resolved = manager.getResolvedBindings();
  const ids = Object.keys(resolved);
  return {
    resolved,
    definitions: Object.fromEntries(ids.map((id) => { const d = manager.getDefinition(id); return [id, { description: d.description ?? null, defaultKeys: d.defaultKeys ?? null }]; })),
    keys: Object.fromEntries(ids.map((id) => [id, manager.getKeys(id)])),
    matches: Object.fromEntries(ids.map((id) => [id, inputs.map((data) => manager.matches(data, id))])),
    conflicts: manager.getConflicts().map((c) => ({ key: c.key, keybindings: [...c.keybindings] })),
    // Pig holds every user binding as a key list, so a lone key and a one-element array are the same value.
    user: Object.fromEntries(Object.entries(manager.getUserBindings()).map(([id, value]) => [id, Array.isArray(value) ? value : [value]])),
  };
};
const results = JSON.parse(input).map((test) => {
  const manager = new KeybindingsManager(TUI_KEYBINDINGS, test.first);
  const before = snapshot(manager);
  manager.setUserBindings(test.second);
  return { before, after: snapshot(manager), inputs };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
