// Run the installed Pi's listModels (cli/list-models.ts) over a stub model runtime, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "0";
const { listModels } = await import(pathToFileURL(root + "dist/cli/list-models.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const test of JSON.parse(input)) {
  const lines = [];
  const realLog = console.log, realError = console.error;
  console.log = (...parts) => lines.push(parts.join(" "));
  console.error = () => {};
  const models = (test.models ?? []).map((m) => ({ provider: m.provider, id: m.id, contextWindow: m.contextWindow, maxTokens: m.maxTokens, reasoning: m.reasoning, input: m.input }));
  await listModels({ getError: () => undefined, getAvailable: async () => models }, test.search);
  console.log = realLog; console.error = realError;
  results.push(lines);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
