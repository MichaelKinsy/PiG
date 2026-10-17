// Drive the installed Pi's NestedCallRecorder over scripted call sequences, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { NestedCallRecorder } = await import(pathToFileURL(root + "dist/core/nested-tool-calls.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((ops) => {
  const recorder = new NestedCallRecorder();
  const records = [];
  const started = [];
  for (const op of ops) {
    if (op.op === "start") { const r = recorder.start(op.call); records.push(r); started.push(r !== undefined); }
    else if (op.op === "finish") recorder.finish(records[op.index], op.isError, op.text);
    else if (op.op === "usage") recorder.addUsage(op.usage);
  }
  const snapshot = recorder.snapshot();
  if (snapshot) for (const call of snapshot.calls) delete call.durationMs;
  // The UTF-16 units of each error: stdout would turn a lone surrogate into U+FFFD.
  const errorUnits = snapshot ? snapshot.calls.map((call) => (call.error === undefined ? null : Array.from({ length: call.error.length }, (_, i) => call.error.charCodeAt(i)))) : [];
  return { started, snapshot: snapshot ?? null, usage: recorder.totalUsage ?? null, errorUnits };
})));
