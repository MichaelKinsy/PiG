// Drive the installed Pi renderHighlightedHtml (utils/syntax-highlight.ts) directly, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { renderHighlightedHtml } = await import(pathToFileURL(root + "dist/utils/syntax-highlight.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  // A formatter wraps its text in <key>...</key>, so the result shows which scope's formatter ran on each run of text.
  const theme = {};
  for (const key of probe.theme ?? []) theme[key] = (text) => `<${key}>${text}</${key}>`;
  try {
    return { out: renderHighlightedHtml(probe.html, theme) };
  } catch (e) {
    return { error: String(e && e.message).slice(0, 80) };
  }
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
