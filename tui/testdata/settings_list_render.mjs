// Drive the installed pi-tui SettingsList through keys and renders, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const { SettingsList, setCapabilities, setKeybindings, TUI_KEYBINDINGS } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
const { KeybindingsManager } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/keybindings.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const sgr = (open, close) => (text) => `\x1b[${open}m${text}\x1b[${close}m`;
const theme = {
  label: (text, selected) => (selected ? sgr(1, 22)(text) : sgr(34, 39)(text)),
  value: (text, selected) => (selected ? sgr(7, 27)(text) : sgr(32, 39)(text)),
  description: sgr(2, 22),
  cursor: "> ",
  hint: sgr(3, 23),
};
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  setKeybindings(new KeybindingsManager(TUI_KEYBINDINGS, {}));
  const items = test.items.map((item) => ({ id: item.id, label: item.label, currentValue: item.value, ...(item.description ? { description: item.description } : {}) }));
  const list = new SettingsList(items, test.maxVisible, theme, () => {}, () => {}, { enableSearch: test.search });
  const frames = [test.widths.map((w) => list.render(w))];
  for (const key of test.keys) {
    list.handleInput(key);
    frames.push(test.widths.map((w) => list.render(w)));
  }
  return frames;
});
process.stdout.write(JSON.stringify(results));
