// Run the installed Pi's runMigrations over fixture directory trees and report the resulting trees and return values.
import { mkdtempSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative, sep } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { runMigrations } = await import(pathToFileURL(root + "dist/migrations.js"));
console.log = (...args) => console.error(...args);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
function snapshot(dir) {
  const out = {};
  const walk = (current) => {
    for (const name of readdirSync(current).sort()) {
      const path = join(current, name);
      const rel = relative(dir, path).split(sep).join("/");
      if (statSync(path).isDirectory()) { out[rel + "/"] = null; walk(path); }
      else out[rel] = readFileSync(path, "latin1") + (rel === "agent/auth.json" ? ` mode=${(statSync(path).mode & 0o777).toString(8)}` : "");
    }
  };
  walk(dir);
  return out;
}
const results = [];
for (const fixture of JSON.parse(input)) {
  const base = mkdtempSync(join(tmpdir(), "pi-migrate-"));
  const agentDir = join(base, "agent");
  const cwd = join(base, "project");
  mkdirSync(agentDir, { recursive: true });
  mkdirSync(cwd, { recursive: true });
  for (const [path, content] of Object.entries(fixture)) {
    const target = join(base, path);
    if (content === null) { mkdirSync(target, { recursive: true }); continue; }
    mkdirSync(join(target, ".."), { recursive: true });
    writeFileSync(target, Buffer.from(content, "latin1"));
  }
  process.env.PI_CODING_AGENT_DIR = agentDir;
  const { migratedAuthProviders, deprecationWarnings } = runMigrations(cwd);
  results.push({ providers: migratedAuthProviders, warnings: deprecationWarnings, tree: snapshot(base) });
}
process.stdout.write(JSON.stringify(results));
