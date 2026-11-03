// Run the installed Pi's InteractiveMode.showExtensionError (interactive-mode.ts, the message and its renderStack block) over a recording chat container, never a translated oracle.
// Each probe is rendered at its widths, switched to the light theme (which invalidates the component tree, as the theme controller does) and rendered again: the stack block is a ThemedText, so it takes the active theme when it renders again.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { Container, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { InteractiveMode } = await load("dist/modes/interactive/interactive-mode.js");
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  initTheme("dark");
  let renders = 0;
  const chatContainer = new Container();
  const target = { chatContainer, ui: { requestRender() { renders++; } } };
  InteractiveMode.prototype.showExtensionError.call(target, probe.path, probe.error, probe.stack === null ? undefined : probe.stack);
  const dark = probe.widths.map((width) => chatContainer.render(width));
  initTheme("light");
  chatContainer.invalidate(); // a theme change invalidates the component tree, which drops the cached rows
  const light = probe.widths.map((width) => chatContainer.render(width));
  results.push({ dark, light, children: chatContainer.children.length, renders });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
