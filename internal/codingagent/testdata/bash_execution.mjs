// Drive the installed Pi's BashExecutionComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { BashExecutionComponent } = await load("dist/modes/interactive/components/bash-execution.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const strip = (line) => line.replace(/\x1b\[[0-9;]*m/g, "").trimEnd();
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const ui = { requestRender() {} };
  const component = new BashExecutionComponent(test.command, ui, test.exclude, test.pad);
  for (const chunk of test.chunks ?? []) component.appendOutput(chunk);
  if (test.complete) component.setComplete(test.complete.exit ?? undefined, test.complete.cancelled, test.complete.truncated ? { truncated: true } : undefined, test.complete.path || undefined);
  const frames = [];
  for (const expanded of [false, true]) {
    component.setExpanded(expanded);
    frames.push(component.render(test.width));
  }
  component.loader.stop();
  return { frames, output: component.getOutput() };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
