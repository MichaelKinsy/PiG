// Compare the installed Pi implementation, never a translated test oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { OAuthSelectorComponent } = await load("dist/modes/interactive/components/oauth-selector.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const strip = (line) => line.replace(/\x1b\[[0-9;]*m/g, "").replace(/\x1b\]8;;[^\x07\x1b]*(\x07|\x1b\\)/g, "").trimEnd();
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  const component = new OAuthSelectorComponent(
    test.mode,
    test.providers,
    (id, authType) => events.push(`select ${id}/${authType}`),
    () => events.push("cancel"),
    test.search,
  );
  const states = [];
  for (const key of test.keys) {
    component.handleInput(key);
    states.push({ events: [...events], lines: component.render(80).map(strip) });
    if (events.length > 0) break;
  }
  return { states };
});
process.stdout.write(JSON.stringify(results));
