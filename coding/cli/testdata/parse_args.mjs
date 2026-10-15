// Run the installed Pi's parseArgs (cli/args.ts) over argument vectors, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { parseArgs } = await import(pathToFileURL(root + "dist/cli/args.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((args) => {
  const a = parseArgs(args);
  const out = {};
  for (const [k, v] of Object.entries(a)) {
    if (v === undefined) continue;
    out[k] = k === "unknownFlags" ? [...v.entries()] : v;
  }
  return out;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
