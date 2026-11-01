// Drive the installed Pi's truncateToVisualLines and VisualLinePreview.render, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { truncateToVisualLines, VisualLinePreview } = await import(pathToFileURL(root + "dist/modes/interactive/components/visual-truncate.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const formatHint = (hidden) => `\x1b[2m... ${hidden} more visual lines, expand to see all of them at once\x1b[22m`;
process.stdout.write(JSON.stringify(JSON.parse(input).map((t) => ({
	...truncateToVisualLines(t.text, t.max, t.width, t.pad, t.keep),
	preview: new VisualLinePreview({ text: t.text, maxVisualLines: t.max, keep: t.keep, formatHint }).render(t.width),
}))));
