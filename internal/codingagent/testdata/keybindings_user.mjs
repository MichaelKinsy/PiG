// Drive Pi 1.1.0's coding-agent KeybindingsManager and keyText with user keybinding configs.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const pi = { ...(await import(new URL("dist/core/keybindings.js", root).href)), ...(await import(new URL("dist/modes/interactive/components/keybinding-hints.js", root).href)) };
const tui = await import(pathToFileURL(new URL("node_modules/@earendil-works/pi-tui/dist/index.js", root).pathname).href);
let input = "";
for await (const chunk of process.stdin) input += chunk;
const { configs, actions, inputs } = JSON.parse(input);
const out = configs.map((config) => {
	const manager = new pi.KeybindingsManager(config);
	tui.setKeybindings(manager);
	const keys = {}, text = {}, matches = {};
	for (const action of actions) {
		keys[action] = manager.getKeys(action);
		text[action] = pi.keyText(action);
		matches[action] = inputs.map((data) => manager.matches(data, action));
	}
	const conflicts = manager.getConflicts().map((c) => ({ key: c.key, keybindings: [...c.keybindings].sort() }));
	return { keys, text, matches, conflicts };
});
process.stdout.write(JSON.stringify(out));
