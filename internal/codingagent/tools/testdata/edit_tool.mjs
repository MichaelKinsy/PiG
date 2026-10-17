// Drive the installed Pi's edit tool (core/tools/edit.ts) over a virtual file system with seeded files and edit requests: exact and fuzzy matching, ambiguous, missing and overlapping targets, multiple edits, legacy top-level oldText/newText, line endings, BOM, and the diff, patch, first changed line and final file content each leaves.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createEditTool } = await import(pathToFileURL(root + "dist/core/tools/edit.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const scenario of JSON.parse(input)) {
  const files = new Map(Object.entries(scenario.files));
  const operations = {
    access: async (p) => { if (!files.has(p)) { const e = new Error(`ENOENT: no such file or directory, access '${p}'`); e.code = "ENOENT"; throw e; } },
    readFile: async (p) => Buffer.from(files.get(p), "utf8"),
    writeFile: async (p, content) => { files.set(p, content); },
  };
  const tool = createEditTool("/vfs", { operations });
  let out;
  try {
    const params = tool.prepareArguments ? tool.prepareArguments(scenario.args) : scenario.args;
    const result = await tool.execute("call", params);
    out = { text: result.content.map((c) => c.text).join(""), details: result.details ? { diff: result.details.diff, patch: result.details.patch, firstChangedLine: result.details.firstChangedLine ?? null } : null };
  } catch (error) { out = { thrown: String(error && error.message) }; }
  results.push({ out, files: Object.fromEntries(files) });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
