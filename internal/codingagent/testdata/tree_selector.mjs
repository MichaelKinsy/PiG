// Drive the installed Pi's TreeSelectorComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { TreeSelectorComponent } = await load("dist/modes/interactive/components/tree-selector.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const toNode = (n) => ({ entry: n.entry, children: n.children.map(toNode), label: n.label || undefined, labelTimestamp: n.labelTimestamp || undefined });
const results = [];
for (const test of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  const component = new TreeSelectorComponent(
    test.tree.map(toNode), test.leaf || null, test.height,
    (id) => events.push("select:" + id),
    () => events.push("cancel"),
    (id, label) => events.push(`label:${id}:${label ?? ""}`),
    test.initialSelected || undefined, test.filter || undefined,
  );
  component.onCopy = (text) => events.push("copy:" + (text ?? ""));
  component.focused = true;
  const steps = [events.slice()], frames = [component.render(test.width)];
  for (const key of test.keys) {
    component.handleInput(key);
    steps.push(events.slice());
    frames.push(component.render(test.width));
  }
  results.push({ steps, frames });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
