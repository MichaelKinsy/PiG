// Drive the installed Pi's BashExecutionComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
globalThis.setInterval = () => 0;
globalThis.clearInterval = () => {};
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { BashExecutionComponent } = await load("dist/modes/interactive/components/bash-execution.js");
// Concatenate the raw chunks before decoding: a pipe can split a multi-byte character between two chunks.
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const results = JSON.parse(Buffer.concat(chunks).toString("utf8")).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const block = new BashExecutionComponent(test.command, { requestRender() {} }, test.excludeFromContext, test.outputPad);
  for (const chunk of test.chunks ?? []) block.appendOutput(chunk);
  if (test.expanded) block.setExpanded(true);
  if (test.complete) {
    const truncation = test.complete.truncated ? { truncated: true } : undefined;
    block.setComplete(test.complete.exitCode ?? undefined, test.complete.cancelled, truncation, test.complete.fullOutputPath || undefined);
  }
  return block.render(test.width);
});
process.stdout.write(JSON.stringify(results));
