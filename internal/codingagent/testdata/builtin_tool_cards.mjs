// Drive the installed Pi's ToolExecutionComponent with its built-in tool definitions, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { ToolExecutionComponent } = await load("dist/modes/interactive/components/tool-execution.js");
const { createAllToolDefinitions } = await load("dist/core/tools/index.js");
let input = "";
// The bash card's clock (renderers/bash.ts createShellRenderers) reads Date.now(): each probe starts it at the
// Go test's toolCardClockStart and moves it by elapsedMs before the result lands, so the row shows that span on both sides.
const clockStart = 1_700_000_000_000;
let now = clockStart;
Date.now = () => now;
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const test of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  now = clockStart;
  const defs = createAllToolDefinitions(test.cwd);
  // A probe that settles waits for the edit call's diff preview, which Pi computes after the arguments complete and then
  // announces with context.invalidate(), the ui's requestRender. A fixed delay lost that race on a loaded runner and
  // drew the pending card.
  let rendered = () => {};
  const component = new ToolExecutionComponent(test.tool, "id1", test.args, { showImages: !test.noImages }, defs[test.tool], { requestRender() { rendered(); } }, test.cwd);
  component.markExecutionStarted();
  component.setArgsComplete();
  if (test.settle) {
    // Earlier requests come from construction; the preview's own invalidate is the one after the first render starts it.
    const repaint = new Promise((resolve) => { rendered = resolve; });
    component.render(test.width);
    await repaint;
  }
  now = clockStart + (test.elapsedMs ?? 0);
  if (test.result) component.updateResult(test.result, test.partial === true);
  component.setExpanded(test.expanded);
  results.push(component.render(test.width));
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
