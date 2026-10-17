// Drive the installed Pi's FileModelsStore over scripted writes and deletes and report the file bytes afterwards.
import { mkdtempSync, readFileSync, writeFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { FileModelsStore } = await import(pathToFileURL(root + "dist/core/models-store.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const script of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "pi-models-"));
  const path = join(dir, "models-store.json");
  if (script.initial !== null) writeFileSync(path, script.initial);
  const store = new FileModelsStore(path);
  for (const op of script.ops) {
    if (op.op === "delete") await store.delete(op.provider);
    else await store.write(op.provider, JSON.parse(op.entry));
  }
  let text = null;
  try { text = readFileSync(path, "utf8"); } catch {}
  results.push(text);
}
process.stdout.write(JSON.stringify(results));
