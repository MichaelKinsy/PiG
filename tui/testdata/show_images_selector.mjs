// Drive the installed Pi's ShowImagesSelectorComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ShowImagesSelectorComponent } = await load("dist/modes/interactive/components/show-images-selector.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  const component = new ShowImagesSelectorComponent(test.current, (show) => events.push("select:" + show), () => events.push("cancel"));
  const frames = [component.render(test.width)];
  const steps = [];
  for (const key of test.keys ?? []) {
    component.getSelectList().handleInput(key);
    steps.push(events.slice());
    frames.push(component.render(test.width));
  }
  return { steps, frames };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
