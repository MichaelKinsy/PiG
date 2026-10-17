// Print the data members of Pi's createXToolDefinition results (function members are omitted, as JSON.stringify does).
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const tools = await import(pathToFileURL(root + "dist/core/tools/index.js"));
const cwd = process.argv[3];
const out = {};
for (const name of ["Read", "Bash", "Edit", "Write", "Grep", "Find", "Ls", "PowerShell"]) {
  out[name] = JSON.parse(JSON.stringify(tools["create" + name + "ToolDefinition"](cwd)));
}
process.stdout.write(JSON.stringify(out), () => process.exit(0));
