// Drive the installed Pi's CustomEditor (app keybinding actions over the editor) with seeded key sequences: which handler each key reaches, the escape, exit and paste hooks, extension shortcuts, autocomplete, text and cursor after every key, insertTextAtCursor, and the rendered frame.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, getEditorTheme } = await load("dist/modes/interactive/theme/theme.js");
const { CustomEditor } = await load("dist/modes/interactive/components/custom-editor.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const flush = () => new Promise((resolve) => setImmediate(resolve));
const results = [];
for (const probe of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  const keybindings = new KeybindingsManager(probe.bindings ?? {});
  setKeybindings(keybindings);
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new CustomEditor(tui, getEditorTheme(), keybindings, { paddingX: probe.paddingX });
  editor.focused = true;
  editor.setAutocompleteProvider({
    getSuggestions: async (lines, cursorLine, cursorCol) => {
      const before = lines[cursorLine].slice(0, cursorCol);
      if (!/^\/\S*$/.test(before)) return null;
      const items = ["alpha", "alpine", "beta", "clear", "model"].filter((n) => n.startsWith(before.slice(1))).map((n) => ({ value: n, label: n }));
      return items.length > 0 ? { items, prefix: before } : null;
    },
    applyCompletion: (lines, cursorLine, cursorCol, item, prefix) => {
      const line = lines[cursorLine];
      const start = Math.max(0, cursorCol - prefix.length);
      const out = lines.slice();
      out[cursorLine] = line.slice(0, start) + "/" + item.value + " " + line.slice(cursorCol);
      return { lines: out, cursorLine, cursorCol: start + 1 + item.value.length + 1 };
    },
  });
  const calls = [];
  for (const action of probe.actions) editor.onAction(action, () => calls.push("action:" + action));
  if (probe.onEscape) editor.onEscape = () => calls.push("onEscape");
  if (probe.onCtrlD) editor.onCtrlD = () => calls.push("onCtrlD");
  if (probe.onPasteImage) editor.onPasteImage = () => calls.push("onPasteImage");
  if (probe.shortcut) editor.onExtensionShortcut = (data) => { calls.push("shortcut"); return data === probe.shortcut; };
  const frames = [];
  for (const op of probe.ops) {
    calls.length = 0;
    if (op.keys !== undefined) editor.handleInput(op.keys);
    else if (op.insert !== undefined) editor.insertTextAtCursor(op.insert);
    else if (op.replaceAction !== undefined) editor.onAction(op.replaceAction, () => calls.push("replaced:" + op.replaceAction));
    await flush();
    await flush();
    frames.push({ calls: [...calls], text: editor.getText(), cursor: editor.getCursor(), autocomplete: editor.isShowingAutocomplete(), rows: editor.render(op.width) });
  }
  results.push(frames);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
