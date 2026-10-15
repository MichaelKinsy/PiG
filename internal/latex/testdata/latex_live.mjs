// Drive the installed pi-tui renderLatex over a corpus, inline and display, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { renderLatex } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/latex.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const run = (source, display) => {
  try { return renderLatex(source, { display }) ?? null; } catch (error) { return { error: String(error && error.message) }; }
};
process.stdout.write(JSON.stringify(JSON.parse(input).map((source) => [run(source, false), run(source, true)])));
