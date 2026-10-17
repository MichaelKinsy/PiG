// Run the installed Pi's grep tool (core/tools/grep.ts, over ripgrep) on a real directory tree with seeded patterns, globs, case and literal flags, context lines, search paths and limits.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createGrepTool } = await import(pathToFileURL(root + "dist/core/tools/grep.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.env.PI_OFFLINE = "1";
const { tree, scenarios } = JSON.parse(input);
const tool = createGrepTool(tree);
const results = [];
for (const args of scenarios) {
  let out;
  try {
    const r = await tool.execute("call", args);
    const d = r.details;
    out = { text: r.content.map((c) => c.text).join(""), matchLimitReached: d?.matchLimitReached ?? null, linesTruncated: d?.linesTruncated ?? false, truncated: d?.truncation ? { by: d.truncation.truncatedBy ?? null, totalLines: d.truncation.totalLines, totalBytes: d.truncation.totalBytes, outputLines: d.truncation.outputLines, outputBytes: d.truncation.outputBytes, maxLines: d.truncation.maxLines, maxBytes: d.truncation.maxBytes } : null };
  } catch (error) { out = { thrown: String(error && error.message) }; }
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
