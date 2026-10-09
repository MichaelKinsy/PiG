// Drive the installed Pi's SessionSelectorComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { SessionSelectorComponent } = await load("dist/modes/interactive/components/session-selector.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const tick = async () => { for (let i = 0; i < 5; i++) await new Promise((resolve) => setImmediate(resolve)); };
const toInfo = (s) => ({ ...s, created: new Date(s.modified), modified: new Date(s.modified) });
const results = [];
for (const test of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  const keybindings = new KeybindingsManager(test.bindings ?? {});
  setKeybindings(keybindings);
  const events = [];
  const component = new SessionSelectorComponent(
    async () => test.current.map(toInfo),
    async () => test.all.map(toInfo),
    (path) => events.push("select:" + path),
    () => events.push("cancel"),
    () => events.push("exit"),
    () => {},
    test.rename ? { renameSession: async (path, name) => { events.push(`rename:${path}:${name}`); }, keybindings } : { keybindings },
    test.currentPath,
  );
  component.focused = true;
  await tick();
  const steps = [], frames = [component.render(test.width)];
  for (const key of test.keys) {
    component.handleInput(key);
    await tick();
    steps.push(events.slice());
    frames.push(component.render(test.width));
  }
  results.push({ steps, frames });
}
// Status timeouts keep the event loop alive, so exit once stdout has drained.
process.stdout.write(JSON.stringify(results), () => process.exit(0));
