// Drive the installed Pi's ToolExecutionComponent with the codemode tool definition's renderers (codemode/renderer.ts), never a translated oracle.
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
const { codemodeRenderers } = await load("dist/extensions/codemode/renderer.js");
let input = "";
// The card's Elapsed and Took rows (core/tools/render-utils.ts toolTimerText) read Date.now(): each probe starts the clock at the
// Go test's toolCardClockStart and moves it by elapsedMs before the result lands, so the row shows that span on both sides, not the scheduler's.
const clockStart = 1_700_000_000_000;
let now = clockStart;
Date.now = () => now;
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  now = clockStart;
  const def = { name: "codemode", label: "codemode", description: "", parameters: {}, ...codemodeRenderers };
  const component = new ToolExecutionComponent(test.tool, "id1", test.args, { showImages: !test.noImages }, def, { requestRender() {} }, test.cwd);
  component.markExecutionStarted();
  component.setArgsComplete();
  now = clockStart + (test.elapsedMs ?? 0);
  if (test.result) component.updateResult(test.result, test.partial === true);
  component.setExpanded(test.expanded);
  return component.render(test.width);
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
