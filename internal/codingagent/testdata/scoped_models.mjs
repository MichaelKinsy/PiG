// Drive the installed Pi's ScopedModelsSelectorComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ScopedModelsSelectorComponent } = await load("dist/modes/interactive/components/scoped-models-selector.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  let enabled = test.enabled, saved = null, didSave = false, cancelled = false;
  const component = new ScopedModelsSelectorComponent(
    { allModels: test.models, enabledModelIds: test.enabled, refreshStatus: test.refreshStatus || undefined },
    { onChange: (ids) => { enabled = ids; }, onPersist: (ids) => { saved = ids; didSave = true; }, onCancel: () => { cancelled = true; } });
  component.focused = true;
  const steps = [{ enabled, saved, didSave, cancelled }], frames = [component.render(test.width)];
  let crash = null;
  for (const key of test.keys) {
    saved = null; didSave = false;
    try {
      component.handleInput(key);
      steps.push(JSON.parse(JSON.stringify({ enabled, saved, didSave, cancelled })));
      frames.push(component.render(test.width));
    } catch (error) {
      // Pi throws when a reorder moves the selection above the first filtered row (selectedIndex -1); nothing can match a crash.
      crash = String(error);
      break;
    }
  }
  return { steps, frames, crash };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
