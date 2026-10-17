// Run the installed Pi's formatDimensionNote (utils/image-resize.ts) over seeded resize results and report the note, or null.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { formatDimensionNote } = await import(pathToFileURL(root + "dist/utils/image-resize.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((r) => formatDimensionNote({ data: "", mimeType: "image/png", ...r }) ?? null)), () => process.exit(0));
