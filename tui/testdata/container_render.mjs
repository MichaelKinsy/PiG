// Drive the installed pi-tui Container and MouseRegion through child edits, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Container, Text, Spacer, MouseRegion } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((ops) => {
  const root = new Container();
  const nested = new Container();
  const leaves = [];
  const region = new MouseRegion(new Text("region child", 1, 0), () => undefined);
  let nestedAttached = false;
  const frames = [];
  for (const op of ops) {
    switch (op.op) {
      case "add": { const leaf = new Text(op.text, op.padX, 0); leaves.push(leaf); root.addChild(leaf); break; }
      case "spacer": root.addChild(new Spacer(op.n)); break;
      case "setText": leaves[op.index]?.setText(op.text); break;
      case "remove": { const leaf = leaves[op.index]; if (leaf) root.removeChild(leaf); break; }
      case "clear": root.clear(); leaves.length = 0; nestedAttached = false; break;
      case "attachNested": if (!nestedAttached) { root.addChild(nested); nestedAttached = true; } break;
      case "nestedAdd": nested.addChild(new Text(op.text, 0, 0)); break;
      case "nestedClear": nested.clear(); break;
      case "attachRegion": root.addChild(region); break;
      case "invalidate": root.invalidate(); break;
      case "render": frames.push(root.render(op.width)); break;
    }
  }
  return frames;
});
process.stdout.write(JSON.stringify(results));
