// Drive the installed coding-agent KeybindingsManager (tui.* plus app.* actions), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const load = (path) => import(pathToFileURL(root + path));
const tui = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager, KEYBINDINGS } = await load("dist/core/keybindings.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const request = JSON.parse(input);
const actions = Object.keys(KEYBINDINGS);
const results = request.probes.map((probe) => {
  tui.setKittyProtocolActive(probe.kitty);
  const manager = new KeybindingsManager(probe.bindings ?? {});
  const matches = {};
  for (const action of actions) {
    const hits = [];
    request.candidates.forEach((data, index) => { if (manager.matches(data, action)) hits.push(index); });
    matches[action] = hits;
  }
  return { matches, keys: Object.fromEntries(actions.map((action) => [action, manager.getKeys(action)])), conflicts: manager.getConflicts() };
});
process.stdout.write(JSON.stringify({ actions, results }), () => process.exit(0));
