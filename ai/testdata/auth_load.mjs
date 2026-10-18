// Drive the installed Pi's AuthStorage over a corrupted auth.json: create, list, read, modify and delete, with the file rewritten between calls. Reports each call's result or error message and the final bytes.
import { mkdtempSync, readFileSync, writeFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
// A `!command` credential's execSync loses its ten-second timeout, which a host slow to start a shell exceeds: the comparison is of what resolves, not of how fast the host is.
const childProcess = (await import("node:child_process")).default;
const { syncBuiltinESMExports } = await import("node:module");
const execSync = childProcess.execSync;
childProcess.execSync = (command, options) => execSync(command, { ...options, timeout: undefined });
syncBuiltinESMExports();
const { AuthStorage } = await import(pathToFileURL(root + "dist/core/auth-storage.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const script of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "pi-authload-"));
  const path = join(dir, "auth.json");
  if (script.initial !== null) writeFileSync(path, script.initial);
  const storage = AuthStorage.create(path);
  const outcomes = [];
  for (const op of script.ops) {
    try {
      if (op.op === "overwrite") { writeFileSync(path, op.text); outcomes.push("ok"); }
      else if (op.op === "list") outcomes.push(JSON.stringify(await storage.list()));
      else if (op.op === "read") outcomes.push(JSON.stringify((await storage.read(op.provider)) ?? null));
      else if (op.op === "delete") { await storage.delete(op.provider); outcomes.push("ok"); }
      else outcomes.push(JSON.stringify((await storage.modify(op.provider, () => ({ type: "api_key", key: op.key }))) ?? null));
    } catch (error) {
      outcomes.push(`error ${error.message}`);
    }
  }
  let text = null;
  try { text = readFileSync(path, "utf8"); } catch {}
  results.push({ outcomes, text });
}
process.stdout.write(JSON.stringify(results));
