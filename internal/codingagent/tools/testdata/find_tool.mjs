// Run the installed Pi's find tool (core/tools/find.ts, over fd) on a real directory tree with seeded patterns, search directories and limits: basename and path globs, hidden files, .gitignore handling inside and outside a git repository, the result limit and the byte limit.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createFindTool } = await import(pathToFileURL(root + "dist/core/tools/find.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.env.PI_OFFLINE = "1";
const { tree, scenarios } = JSON.parse(input);
const tool = createFindTool(tree);
const results = [];
for (const args of scenarios) {
  let out;
  try {
    const r = await tool.execute("call", args);
    const d = r.details;
    out = { text: r.content.map((c) => c.text).join(""), resultLimitReached: d?.resultLimitReached ?? null, truncated: d?.truncation ? { by: d.truncation.truncatedBy ?? null, totalLines: d.truncation.totalLines, totalBytes: d.truncation.totalBytes, outputLines: d.truncation.outputLines, outputBytes: d.truncation.outputBytes, maxLines: d.truncation.maxLines, maxBytes: d.truncation.maxBytes } : null };
  } catch (error) { out = { thrown: String(error && error.message) }; }
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
