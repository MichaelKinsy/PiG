// Run the installed Pi's processFileArguments (cli/file-processor.ts) in a directory, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.chdir(process.argv[3]);
const { processFileArguments } = await import(pathToFileURL(root + "dist/cli/file-processor.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const strip = (s) => s.replace(/\x1b\[[0-9;]*m/g, "");
const results = [];
for (const arg of JSON.parse(input)) {
  let error = "";
  const realError = console.error, realExit = process.exit;
  console.error = (message) => { error = strip(String(message)); };
  process.exit = () => { throw new Error("exit"); };
  let text = "";
  try { text = (await processFileArguments([arg])).text; } catch (e) { if (e.message !== "exit") error = String(e); }
  console.error = realError; process.exit = realExit;
  results.push({ text, error });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
