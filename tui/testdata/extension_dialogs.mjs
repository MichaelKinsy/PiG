// Compare the installed Pi implementation, never a translated test oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
// Chalk's bold helper detects a pipe rather than a terminal unless color is forced.
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ExtensionSelectorComponent } = await load("dist/modes/interactive/components/extension-selector.js");
const { ExtensionInputComponent } = await load("dist/modes/interactive/components/extension-input.js");
// CountdownTimer's interval callback runs when a probe sends "<tick>".
let intervals = [];
globalThis.setInterval = (callback) => intervals.push(callback);
globalThis.clearInterval = (id) => { intervals[id - 1] = undefined; };
let input = "";
for await (const chunk of process.stdin) input += chunk;
const cases = JSON.parse(input);
const results = cases.map((test) => {
  setCapabilities({ images: null, trueColor: test.trueColor, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  let done = false, cancelled = false, value = "", toggles = 0;
  const submit = (result) => { done = true; value = result; };
  const cancel = () => { done = true; cancelled = true; };
  intervals = [];
  const title = test.title ?? "Rigidity probe";
  const timed = test.timeout ? { tui: { requestRender() {} }, timeout: test.timeout } : {};
  const component = test.kind === "input"
    ? new ExtensionInputComponent(title, undefined, submit, cancel, timed)
    : new ExtensionSelectorComponent(title, test.options, submit, cancel, { onToggleToolsExpanded: () => toggles++, ...timed });
  component.focused = true;
  // Borders belong to DynamicBorder, not either dialog's content contract.
  const renderContent = () => component.render(test.width).slice(2, -2);
  const frames = [renderContent()];
  const states = [];
  for (const key of test.keys) {
    if (key === "<tick>") intervals.at(-1)?.();
    else component.handleInput(key);
    states.push({ done, cancelled, value, toggles });
    if (done) break;
    frames.push(renderContent());
  }
  return { states, frames };
});
process.stdout.write(JSON.stringify(results));
