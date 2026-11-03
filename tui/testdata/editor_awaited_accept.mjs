// Drive the installed pi-tui Editor with a slash command whose argument completions are awaited (a Promise), moving the cursor with the popup open and accepting it; never a translated oracle.
import { mkdtempSync, readFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { Editor, CombinedAutocompleteProvider, setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme, getEditorTheme } = await load("dist/modes/interactive/theme/theme.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const flush = () => new Promise((resolve) => setImmediate(resolve));
const base = mkdtempSync(join(tmpdir(), "awaited-accept-"));
const skills = ["skill-a", "skill-b", "tool-a"];
const results = [];
for (const probe of JSON.parse(input)) {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager({}));
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, getEditorTheme(), { paddingX: 0, autocompleteMaxVisible: 5 });
  editor.focused = true;
  let callbacks = [];
  editor.onSubmit = (t) => callbacks.push(["submit", t]);
  editor.onChange = (t) => callbacks.push(["change", t]);
  editor.setAutocompleteProvider(
    new CombinedAutocompleteProvider(
      [
        {
          name: "load-skills",
          description: "Load skills",
          getArgumentCompletions: async (prefix) => {
            await flush();
            const items = skills.filter((s) => s.startsWith(prefix)).map((s) => ({ value: s, label: s }));
            return items.length > 0 ? items : null;
          },
        },
      ],
      base,
    ),
  );
  const log = [];
  for (const op of probe.ops) {
    callbacks = [];
    if (op.keys !== undefined) editor.handleInput(op.keys);
    else if (op.setText !== undefined) editor.setText(op.setText);
    for (let i = 0; i < 4; i++) await flush();
    log.push({ text: editor.getText(), cursor: editor.getCursor(), lines: editor.getLines(), callbacks, rows: editor.render(40) });
  }
  results.push(log);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
