// Drive the installed pi-tui Editor under rebound keybindings, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const load = (path) => import(pathToFileURL(root + path));
const { Editor, setKeybindings } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const mark = (s) => s.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, (u) => `\uf8ff${u.charCodeAt(0).toString(16)}`);
const results = JSON.parse(input).map((test) => {
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, { borderColor: (s) => s, selectList: {} }, { paddingX: test.paddingX });
  editor.focused = true;
  let callbacks = [];
  editor.onSubmit = (t) => callbacks.push(["submit", mark(t)]);
  editor.onChange = (t) => callbacks.push(["change", mark(t)]);
  const log = [];
  for (const op of test.ops) {
    callbacks = [];
    let rendered = null;
    if (op.keys !== undefined) editor.handleInput(op.keys);
    else if (op.setText !== undefined) editor.setText(op.setText);
    else if (op.history !== undefined) editor.addToHistory(op.history);
    else if (op.render !== undefined) rendered = editor.render(op.render).map(mark);
    log.push({ text: mark(editor.getText()), expanded: mark(editor.getExpandedText()), cursor: editor.getCursor(), lines: editor.getLines().map(mark), callbacks, rendered });
  }
  return log;
});
process.stdout.write(JSON.stringify(results));
