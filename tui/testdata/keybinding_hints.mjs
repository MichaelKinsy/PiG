// Drive the installed Pi's keybinding-hints.ts helpers, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const hints = await load("dist/modes/interactive/components/keybinding-hints.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  return {
    format: hints.formatKeyText(test.key),
    formatCap: hints.formatKeyText(test.key, { capitalize: true }),
    raw: hints.rawKeyHint(test.key, test.description),
    keyText: hints.keyText(test.action),
    keyDisplay: hints.keyDisplayText(test.action),
    hint: hints.keyHint(test.action, test.description),
  };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
