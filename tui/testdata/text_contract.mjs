// Drive the installed pi-tui Text through sequences of render, setText, setPaddingX, setCustomBgFn and invalidate calls, recording the rows of every
// render and how often the background function had been called by then, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Text } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  let bgCalls = 0;
  const bg = (text) => {
    bgCalls++;
    return `<bg>${text}</bg>`;
  };
  const text = new Text(probe.text, probe.paddingX, probe.paddingY, probe.bg ? bg : undefined);
  const renders = [];
  for (const op of probe.ops) {
    switch (op.op) {
      case "render": {
        const rows = text.render(op.width);
        renders.push({ rows, bgCalls });
        break;
      }
      case "setText": text.setText(op.text); break;
      case "setPaddingX": text.setPaddingX(op.paddingX); break;
      case "setBg": text.setCustomBgFn(op.on ? bg : undefined); break;
      case "invalidate": text.invalidate(); break;
      default: throw new Error("unknown op " + op.op);
    }
  }
  return renders;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
