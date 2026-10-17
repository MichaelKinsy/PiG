// Run the installed Pi's editInExternalEditor (modes/interactive/external-editor.ts) with the scripted editors the test wrote, and report the result, the launch message and what the editor recorded.
import { readFileSync, realpathSync, existsSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { editInExternalEditor } = await import(pathToFileURL(root + "dist/modes/interactive/external-editor.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const write = process.stdout.write.bind(process.stdout);
const results = [];
for (const scenario of JSON.parse(input)) {
  process.env.REC = scenario.rec;
  let message = "";
  process.stdout.write = (chunk) => { message += chunk; return true; };
  let result;
  const leftBefore = readdirSync(tmpdir()).filter((n) => n.startsWith("pi-editor-")).length;
  try { result = await editInExternalEditor({ command: scenario.command, content: scenario.content }); } catch (error) { result = { thrown: String(error && error.message) }; }
  process.stdout.write = write;
  const leftAfter = readdirSync(tmpdir()).filter((n) => n.startsWith("pi-editor-")).length;
  results.push({ result, message, recorded: existsSync(scenario.rec) ? readFileSync(scenario.rec, "utf8") : null, leaked: leftAfter - leftBefore });
}
write(JSON.stringify(results), () => process.exit(0));
