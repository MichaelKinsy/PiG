// Drive the installed Pi's ls tool (core/tools/ls.ts) over a virtual file system with seeded directories and requests: ordering (ICU collation, case-insensitive), the directory suffix, unreadable entries and directories, missing paths, files, the entry limit and the byte limit.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createLsTool } = await import(pathToFileURL(root + "dist/core/tools/ls.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const scenario of JSON.parse(input)) {
  const nodes = new Map(Object.entries(scenario.nodes)); // path -> {dir: bool, entries?: string[], unreadable?: bool}
  const operations = {
    exists: async (p) => nodes.has(p),
    stat: async (p) => { const n = nodes.get(p); if (!n) { const e = new Error(`ENOENT: no such file or directory, stat '${p}'`); e.code = "ENOENT"; throw e; } return { isDirectory: () => n.dir }; },
    readdir: async (p) => { const n = nodes.get(p); if (n.unreadable) throw new Error("boom"); return [...n.entries]; },
  };
  const tool = createLsTool("/vfs", { operations });
  let out;
  try {
    const r = await tool.execute("call", scenario.args);
    const d = r.details;
    out = { text: r.content.map((c) => c.text).join(""), entryLimitReached: d?.entryLimitReached ?? null, truncation: d?.truncation ? { truncated: d.truncation.truncated, truncatedBy: d.truncation.truncatedBy ?? null, totalLines: d.truncation.totalLines, totalBytes: d.truncation.totalBytes, outputLines: d.truncation.outputLines, outputBytes: d.truncation.outputBytes, lastLinePartial: d.truncation.lastLinePartial, firstLineExceedsLimit: d.truncation.firstLineExceedsLimit, maxLines: d.truncation.maxLines, maxBytes: d.truncation.maxBytes } : null };
  } catch (error) { out = { thrown: String(error && error.message) }; }
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
