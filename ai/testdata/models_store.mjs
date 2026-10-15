// Run the installed Pi's FileModelsStore (core/models-store.ts) over seeded files and operation sequences: reads of absent, empty, corrupt, BOM and valid files, writes and deletes, and the exact text the file holds after each.
import { readFileSync, realpathSync, mkdtempSync, writeFileSync, rmSync, existsSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { FileModelsStore } = await import(pathToFileURL(root + "dist/core/models-store.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const base = mkdtempSync(join(tmpdir(), "models-store-oracle-"));
const results = [];
try {
  let n = 0;
  for (const scenario of JSON.parse(input)) {
    const dir = join(base, String(n++));
    mkdirSync(dir, { recursive: true });
    const path = join(dir, scenario.nested ? "sub/models-store.json" : "models-store.json");
    if (scenario.initial !== null) { mkdirSync(join(path, ".."), { recursive: true }); writeFileSync(path, scenario.initial); }
    const store = new FileModelsStore(path);
    const outs = [];
    for (const op of scenario.ops) {
      let result = null;
      try {
        if (op.op === "read") { const e = await store.read(op.provider); result = e === undefined ? null : e; }
        else if (op.op === "write") await store.write(op.provider, op.entry);
        else if (op.op === "delete") await store.delete(op.provider);
        else if (op.op === "rewrite") { mkdirSync(join(path, ".."), { recursive: true }); writeFileSync(path, op.text); }
      } catch (error) { result = { thrown: String(error && error.message).replaceAll(dir, "<dir>").slice(0, 120) }; }
      outs.push({ result, file: existsSync(path) ? readFileSync(path, "utf8") : null });
    }
    results.push(outs);
  }
} finally { rmSync(base, { recursive: true, force: true }); }
process.stdout.write(JSON.stringify(results), () => process.exit(0));
