// Drive the installed Pi's AuthStorage over scripted modify/delete calls on an existing auth.json and report the file bytes afterwards.
import { mkdtempSync, readFileSync, writeFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { AuthStorage } = await import(pathToFileURL(root + "dist/core/auth-storage.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const script of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "pi-auth-"));
  const path = join(dir, "auth.json");
  if (script.initial !== null) writeFileSync(path, script.initial);
  const storage = AuthStorage.create(path);
  for (const op of script.ops) {
    if (op.op === "delete") await storage.delete(op.provider);
    else if (op.op === "apiKey") await storage.modify(op.provider, () => (op.env === null ? { type: "api_key", key: op.key } : { type: "api_key", key: op.key, env: op.env }));
    else if (op.op === "shape") {
      // The object literals that build a credential in pi-ai auth/oauth/*.ts.
      const base = { refresh: op.refresh, access: op.access, expires: op.expires };
      const literals = {
        anthropic: () => ({ type: "oauth", refresh: base.refresh, access: base.access, expires: base.expires }),
        copilot: () => ({ type: "oauth", refresh: base.refresh, access: base.access, expires: base.expires, enterpriseUrl: op.extra, availableModelIds: [op.extra, "m"] }),
        kimi: () => ({ type: "oauth", access: base.access, refresh: base.refresh, expires: base.expires }),
        codex: () => ({ type: "oauth", access: base.access, refresh: base.refresh, expires: base.expires, accountId: op.extra }),
        chatgpt: () => ({ type: "oauth", access: base.access, refresh: base.refresh, expires: base.expires, clientId: op.extra, scopes: ["a", op.extra] }),
        radius: () => ({ type: "oauth", access: base.access, refresh: base.refresh, expires: base.expires, scope: op.extra }),
      };
      await storage.modify(op.provider, () => literals[op.shape]());
    } else await storage.modify(op.provider, () => ({ type: "oauth", refresh: op.refresh, access: op.access, expires: op.expires }));
  }
  let text = null;
  try { text = readFileSync(path, "utf8"); } catch {}
  results.push(text);
}
process.stdout.write(JSON.stringify(results));
