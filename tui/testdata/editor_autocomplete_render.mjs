// Drive the installed pi-tui Editor with the coding-agent editor theme and a slash-command autocomplete provider, rendering after every key, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { Editor, setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, getEditorTheme } = await load("dist/modes/interactive/theme/theme.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const flush = () => new Promise((resolve) => setImmediate(resolve));
const results = [];
for (const probe of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(probe.theme);
  setKeybindings(new KeybindingsManager({}));
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, getEditorTheme(), { paddingX: probe.paddingX, autocompleteMaxVisible: probe.maxVisible });
  editor.focused = true;
  const commands = probe.commands;
  editor.setAutocompleteProvider({
    getSuggestions: async (lines, cursorLine, cursorCol) => {
      const before = lines[cursorLine].slice(0, cursorCol);
      if (!/^\/\S*$/.test(before)) return null;
      const items = commands.filter((c) => c.name.startsWith(before.slice(1))).map((c) => (c.description ? { value: c.name, label: c.name, description: c.description } : { value: c.name, label: c.name }));
      return items.length > 0 ? { items, prefix: before } : null;
    },
    applyCompletion: (lines, cursorLine, cursorCol, item, prefix) => {
      const line = lines[cursorLine];
      const start = Math.max(0, cursorCol - prefix.length);
      const next = line.slice(0, start) + "/" + item.value + " " + line.slice(cursorCol);
      const out = lines.slice();
      out[cursorLine] = next;
      return { lines: out, cursorLine, cursorCol: start + 1 + item.value.length + 1 };
    },
  });
  const frames = [];
  for (const op of probe.ops) {
    if (op.keys !== undefined) editor.handleInput(op.keys);
    else if (op.max !== undefined) editor.setAutocompleteMaxVisible(op.max);
    await flush();
    await flush();
    frames.push({ text: editor.getText(), rows: editor.render(op.width) });
  }
  results.push(frames);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
