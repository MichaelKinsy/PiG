// Drive the installed pi-tui SelectList, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const load = (path) => import(pathToFileURL(root + path));
const { SelectList, setKeybindings } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const tag = (name) => (text) => `<${name}>${text}</${name}>`;
const theme = { selectedPrefix: tag("sp"), selectedText: tag("st"), description: tag("d"), scrollInfo: tag("si"), noMatch: tag("nm") };
const results = JSON.parse(input).map((test) => {
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const events = [];
  const list = new SelectList(test.items, test.maxVisible, theme, test.layout ?? {});
  list.onSelect = (item) => events.push("select:" + item.value);
  list.onCancel = () => events.push("cancel");
  list.onSelectionChange = (item) => events.push("change:" + item.value);
  if (test.filter) list.setFilter(test.filter);
  const selected = (list) => list.getSelectedItem()?.value ?? null;
  const steps = [events.slice()], frames = [list.render(test.width)], selectedItems = [selected(list)];
  for (const key of test.keys) {
    list.handleInput(key);
    steps.push(events.slice());
    frames.push(list.render(test.width));
    selectedItems.push(selected(list));
  }
  return { steps, frames, selected: selectedItems };
});
process.stdout.write(JSON.stringify(results));
