// Drive the installed Pi's ExtensionSelectorComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities, setKittyProtocolActive } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ExtensionSelectorComponent } = await load("dist/modes/interactive/components/extension-selector.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKittyProtocolActive(test.kitty === true);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  const component = new ExtensionSelectorComponent(test.title, test.options ?? [], (option) => events.push("select:" + option), () => events.push("cancel"), test.description ? { description: test.description } : undefined);
  const frames = [component.render(test.width)];
  const steps = [];
  for (const key of test.keys ?? []) {
    component.handleInput(key);
    steps.push(events.slice());
    frames.push(component.render(test.width));
  }
  return { steps, frames };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
