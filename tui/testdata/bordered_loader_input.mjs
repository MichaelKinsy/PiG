// Compare the installed Pi implementation, never a translated test oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, theme } = await load("dist/modes/interactive/theme/theme.js");
const { BorderedLoader } = await load("dist/modes/interactive/components/bordered-loader.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const tui = { terminal: { rows: 40, columns: 80 }, requestRender() {} };
  const loader = new BorderedLoader(tui, theme, "Working", { cancellable: test.cancellable });
  let aborts = 0;
  loader.onAbort = () => aborts++;
  const states = [];
  for (const key of test.keys) {
    loader.handleInput(key);
    states.push({ aborted: loader.signal.aborted, aborts });
  }
  loader.dispose();
  return { states };
});
process.stdout.write(JSON.stringify(results));
process.exit(0);
