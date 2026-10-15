// Drive the installed Pi's read, write and ls tools over scripted directory trees and report the result and the tree afterwards, never a translated oracle.
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const tools = await import(pathToFileURL(root + "dist/core/tools/index.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const walk = (dir, prefix = "") => {
  const out = {};
  for (const name of readdirSync(dir).sort()) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) { out[prefix + name + "/"] = ""; Object.assign(out, walk(full, prefix + name + "/")); }
    else out[prefix + name] = readFileSync(full).toString("base64");
  }
  return out;
};
const results = [];
for (const probe of JSON.parse(input)) {
  const dir = join(mkdtempSync(join(tmpdir(), "pi-files-")), "cwd");
  mkdirSync(dir);
  for (const file of probe.files ?? []) {
    mkdirSync(dirname(join(dir, file.path)), { recursive: true });
    if (file.dir) mkdirSync(join(dir, file.path), { recursive: true });
    else writeFileSync(join(dir, file.path), Buffer.from(file.content ?? "", "base64"));
  }
  // grep and find spawn rg and fd without a cwd, so they run in the process's directory: a pi session's process directory is its cwd, and a --glob with a slash is anchored there.
  process.chdir(dir);
  const tool = tools.createToolDefinition(probe.tool, dir);
  let outcome;
  try {
    const args = tool.prepareArguments ? tool.prepareArguments(probe.args) : probe.args;
    const result = await tool.execute("id", args, undefined, undefined, undefined);
    outcome = { content: result.content, details: result.details ?? null };
  } catch (error) { outcome = { thrown: error.message }; }
  outcome.tree = walk(dir);
  outcome.dir = dir;
  results.push(outcome);
}
process.stdout.write(JSON.stringify(results));
