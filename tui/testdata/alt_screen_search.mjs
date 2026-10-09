// Drive the installed pi-tui AltScreenSearchComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { AltScreenSearchComponent } = await load("node_modules/@earendil-works/pi-tui/dist/alt-screen-search.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const mark = (s) => s.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, (u) => `\uf8ff${u.charCodeAt(0).toString(16)}`);
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  setKeybindings(new KeybindingsManager(test.bindings ?? {}));
  let changes = [];
  const component = new AltScreenSearchComponent((q) => changes.push(mark(q)), (text, hovered) => (hovered ? `<${text}>` : `[${text}]`));
  component.focused = test.focused;
  return test.ops.map((op) => {
    changes = [];
    let rendered = null, direction = null, changed = null;
    if (op.keys !== undefined) component.handleInput(op.keys);
    else if (op.result !== undefined) component.setResult(op.result[0], op.result[1]);
    else if (op.hover !== undefined) changed = component.setHoveredNavigationDirection(op.hover === 0 ? undefined : op.hover);
    else if (op.render !== undefined) rendered = component.render(op.render).map(mark);
    else if (op.at !== undefined) direction = component.getNavigationDirectionAt(op.at[0], op.at[1]) ?? 0;
    return { changes, rendered, direction, changed };
  });
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
