// Drive the installed pi-tui Editor with typed characters, with a provider that answers slash commands, a command's arguments and @, # and $ tokens, and log which typed characters open the popup; never a translated oracle.
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
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const commands = ["load-skills", "list", "help"];
const skills = ["skill-a", "skill-b", "éskill-a"];
const tokens = ["alpha", "alpine", "beta"];
const matching = (names, prefix) => names.filter((name) => name.startsWith(prefix)).map((name) => ({ value: name, label: name }));
const provider = {
  triggerCharacters: ["$"],
  getSuggestions: async (lines, cursorLine, cursorCol) => {
    const before = lines[cursorLine].slice(0, cursorCol);
    let match, items;
    if ((match = /^\/(\S*)$/.exec(before))) {
      items = matching(commands, match[1]);
      return items.length > 0 ? { items, prefix: before } : null;
    }
    if ((match = /^\/load-skills (\S*)$/.exec(before))) {
      items = matching(skills, match[1]);
      return items.length > 0 ? { items, prefix: match[1] } : null;
    }
    if ((match = /(?:^|[\s([{<`])([@#$])(\S*)$/.exec(before))) {
      items = matching(tokens, match[2]);
      return items.length > 0 ? { items, prefix: match[1] + match[2] } : null;
    }
    return null;
  },
  applyCompletion: (lines, cursorLine, cursorCol, item, prefix) => {
    const line = lines[cursorLine];
    const start = Math.max(0, cursorCol - prefix.length);
    const out = lines.slice();
    out[cursorLine] = line.slice(0, start) + item.value + line.slice(cursorCol);
    return { lines: out, cursorLine, cursorCol: start + item.value.length };
  },
};
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark");
setKeybindings(new KeybindingsManager({}));
const run = async (probe) => {
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, getEditorTheme(), { paddingX: 0, autocompleteMaxVisible: 5 });
  editor.focused = true;
  let callbacks = [];
  editor.onSubmit = (t) => callbacks.push(["submit", t]);
  editor.onChange = (t) => callbacks.push(["change", t]);
  editor.setAutocompleteProvider(provider);
  const log = [];
  for (const op of probe.ops) {
    callbacks = [];
    if (op.keys !== undefined) editor.handleInput(op.keys);
    else if (op.setText !== undefined) editor.setText(op.setText);
    for (let i = 0; i < 4; i++) await flush();
    // A symbol request waits out the editor's 20 ms debounce timer before it asks the provider.
    await sleep(40);
    for (let i = 0; i < 4; i++) await flush();
    log.push({ text: editor.getText(), cursor: editor.getCursor(), lines: editor.getLines(), callbacks, rows: editor.render(40) });
  }
  return log;
};
const results = await Promise.all(JSON.parse(input).map(run));
process.stdout.write(JSON.stringify(results), () => process.exit(0));
