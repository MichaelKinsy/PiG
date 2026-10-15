// Runs the installed, pinned Pi radius-login-selector.ts over the probes on stdin.
// Each probe selects the last option, then renders at width and elapsed animation time.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { createLoginMenuSelector } = await load("dist/modes/interactive/components/radius-login-selector.js");
let now = 1000;
performance.now = () => now;
globalThis.setInterval = () => 1;
globalThis.clearInterval = () => {};
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  setCapabilities({ images: null, trueColor: probe.trueColor, hyperlinks: false });
  initTheme(probe.theme);
  setKeybindings(new KeybindingsManager({}));
  now = 1000;
  const calls = [];
  const menu = createLoginMenuSelector({ requestRender: () => calls.push("render") }, probe.title, probe.options, probe.radius, () => {}, () => {});
  menu.focused = true;
  for (let i = 0; i < probe.downs; i++) menu.handleInput("\x1b[B");
  now = 1000 + probe.elapsedMs;
  return menu.render(probe.width);
});
process.stdout.write(JSON.stringify(results));
