// Drive the installed Pi's ConfigSelectorComponent (resource list), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { SettingsManager } = await load("dist/core/settings-manager.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ConfigSelectorComponent } = await load("dist/modes/interactive/components/config-selector.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const arrays = (r) => ({ extensions: r.extensions ?? [], skills: r.skills ?? [], prompts: r.prompts ?? [], themes: r.themes ?? [] });
const results = JSON.parse(input).map((test) => {
  test.resolved = { global: arrays(test.resolved.global), project: arrays(test.resolved.project) };
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  let cancelled = 0, exited = 0;
  const component = new ConfigSelectorComponent(test.resolved, SettingsManager.inMemory(), "/work/project", test.agentDir,
    () => { cancelled++; }, () => { exited++; }, () => {}, test.height || undefined, test.writeScope, test.projectModeAvailable);
  component.focused = true;
  const steps = [{ cancelled, exited }], frames = [component.render(test.width)];
  let crash = null;
  for (const key of test.keys) {
    try {
      component.getResourceList().handleInput(key);
      steps.push({ cancelled, exited });
      frames.push(component.render(test.width));
    } catch (error) {
      crash = String(error);
      break;
    }
  }
  return { steps, frames, crash };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
