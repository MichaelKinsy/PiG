// Drive the installed pi-tui Editor with a slash-command and forced file-name autocomplete provider under rebound keybindings, never a translated oracle.
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
const commands = ["alpha", "alpine", "beta", "build", "clear"];
const files = ["alpha.txt", "alpine.go", "beta.md"];
const results = [];
for (const probe of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager(probe.bindings ?? {}));
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, getEditorTheme(), { paddingX: 0, autocompleteMaxVisible: probe.maxVisible ?? 5 });
  editor.focused = true;
  editor.disableSubmit = probe.disableSubmit === true;
  let callbacks = [];
  editor.onSubmit = (t) => callbacks.push(["submit", t]);
  editor.onChange = (t) => callbacks.push(["change", t]);
  editor.setAutocompleteProvider({
    getSuggestions: async (lines, cursorLine, cursorCol, options) => {
      const before = lines[cursorLine].slice(0, cursorCol);
      if (/^\/\S*$/.test(before)) {
        const items = commands.filter((c) => c.startsWith(before.slice(1))).map((c) => ({ value: c, label: c }));
        return items.length > 0 ? { items, prefix: before } : null;
      }
      if (!options.force) return null;
      const token = before.match(/\S*$/)[0];
      const items = files.filter((f) => f.startsWith(token)).map((f) => ({ value: f, label: f }));
      return items.length > 0 ? { items, prefix: token } : null;
    },
    applyCompletion: (lines, cursorLine, cursorCol, item, prefix) => {
      const line = lines[cursorLine];
      const start = Math.max(0, cursorCol - prefix.length);
      const insert = prefix.startsWith("/") ? "/" + item.value + " " : item.value;
      const out = lines.slice();
      out[cursorLine] = line.slice(0, start) + insert + line.slice(cursorCol);
      return { lines: out, cursorLine, cursorCol: start + insert.length };
    },
  });
  const log = [];
  for (const op of probe.ops) {
    callbacks = [];
    if (op.keys !== undefined) editor.handleInput(op.keys);
    else if (op.setText !== undefined) editor.setText(op.setText);
    else if (op.history !== undefined) editor.addToHistory(op.history);
    await flush();
    await flush();
    log.push({ text: editor.getText(), cursor: editor.getCursor(), lines: editor.getLines(), callbacks, rows: editor.render(40) });
  }
  results.push(log);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
