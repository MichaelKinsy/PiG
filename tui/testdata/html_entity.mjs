// Drive the installed Pi's decodeHtmlEntityAt, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const html = await import(pathToFileURL(root + "dist/utils/html.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map(([text, index]) => {
  const r = html.decodeHtmlEntityAt(text, index);
  return r ? { text: r.text, length: r.length } : null;
})));
