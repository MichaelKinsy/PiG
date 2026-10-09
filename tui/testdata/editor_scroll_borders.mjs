// Drive the installed pi-tui Editor through a bracketed paste of N rows and some Up keys, then render: the scroll indicators of both borders, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Editor } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
  const editor = new Editor(tui, { borderColor: (s) => s, selectList: {} }, { paddingX: probe.paddingX });
  editor.focused = true;
  editor.handleInput("\x1b[200~" + Array.from({ length: probe.rows }, (_, i) => `row ${i}`).join("\n") + "\x1b[201~");
  for (let i = 0; i < probe.ups; i++) editor.handleInput("\x1b[A");
  return editor.render(probe.width);
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
