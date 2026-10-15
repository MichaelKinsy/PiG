// Drive the installed pi-tui Input, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const load = (path) => import(pathToFileURL(root + path));
const { Input, setKeybindings } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const mark = (s) => s.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, (u) => `\uf8ff${u.charCodeAt(0).toString(16)}`);
const results = JSON.parse(input).map((test) => {
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  const field = new Input();
  field.focused = !test.unfocused;
  let events = [];
  field.onSubmit = (v) => events.push(["submit", mark(v)]);
  field.onEscape = () => events.push(["escape", ""]);
  return test.ops.map((op) => {
    events = [];
    let rendered = null;
    if (op.keys !== undefined) field.handleInput(op.keys);
    else if (op.setValue !== undefined) field.setValue(op.setValue);
    else if (op.render !== undefined) rendered = field.render(op.render).map(mark);
    return { value: mark(field.getValue()), cursor: field.cursor, events, rendered };
  });
});
process.stdout.write(JSON.stringify(results));
