// Drive the installed Pi's edit tool over scripted files and edits and report the result and the file afterwards, never a translated oracle.
import { mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createEditToolDefinition } = await import(pathToFileURL(root + "dist/core/tools/edit.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "pi-edit-"));
  writeFileSync(join(dir, "f.txt"), Buffer.from(probe.content, "base64"));
  const tool = createEditToolDefinition(dir);
  let outcome;
  try {
    const args = tool.prepareArguments ? tool.prepareArguments(probe.args) : probe.args;
    const result = await tool.execute("id", args, undefined, undefined, undefined);
    outcome = { text: result.content.map((c) => c.text).join(""), details: result.details };
  } catch (error) { outcome = { thrown: error.message }; }
  outcome.file = readFileSync(join(dir, "f.txt")).toString("base64");
  results.push(outcome);
}
process.stdout.write(JSON.stringify(results));
