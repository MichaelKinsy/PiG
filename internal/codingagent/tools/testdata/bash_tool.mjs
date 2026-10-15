// Drive the installed Pi's bash tool over scripted commands and report the result, never a translated oracle.
import { mkdtempSync, readFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createBashToolDefinition } = await import(pathToFileURL(root + "dist/core/tools/bash.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "pi-bash-"));
  const tool = createBashToolDefinition(dir);
  let outcome;
  try {
    const args = tool.prepareArguments ? tool.prepareArguments(probe.args) : probe.args;
    const result = await tool.execute("id", args, undefined, undefined, undefined);
    outcome = { text: result.content.map((c) => c.text).join(""), details: result.details ?? null };
  } catch (error) { outcome = { thrown: error.message }; }
  outcome.dir = dir;
  results.push(outcome);
}
process.stdout.write(JSON.stringify(results));
