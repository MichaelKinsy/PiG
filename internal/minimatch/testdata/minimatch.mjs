// Run the installed Pi's minimatch (10.2.6), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
const { minimatch } = await import(pathToFileURL(root + "node_modules/minimatch/dist/esm/index.js"));
if (JSON.parse(readFileSync(root + "node_modules/minimatch/package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong minimatch version");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map(({ name, pattern, nocase, dot }) => {
  try { return minimatch(name, pattern, { nocase, dot }); } catch (error) { return "threw"; }
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
