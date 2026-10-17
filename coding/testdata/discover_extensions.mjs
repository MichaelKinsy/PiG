// Run the installed Pi's discoverAndLoadExtensions over seeded extension trees (project, agent and configured paths; files, directories with index or package.json manifests, symbolic links, missing paths) and report the extension paths it loaded and those it failed to load, in order. Usage: node discover_extensions.mjs <pi version> <sandbox>; stdin: [{build, cwd, agentDir, configured}], build lists {kind,path,content|target} entries, with {S} for the sandbox and {CFG} for the project config directory.
import { mkdirSync, readFileSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
import { dirname, join, sep } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const sandbox = process.argv[3];
process.env.HOME = join(sandbox, "home");
const { discoverAndLoadExtensions } = await import(pathToFileURL(root + "dist/core/extensions/loader.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const [index, probe] of JSON.parse(input).entries()) {
  const base = join(sandbox, `p${index}`);
  const fill = (text) => text.replaceAll("{S}", base).replaceAll("{CFG}", ".pi");
  mkdirSync(join(base, "home"), { recursive: true });
  process.env.HOME = process.env.USERPROFILE = join(base, "home");
  for (const entry of probe.build) {
    const path = fill(entry.path);
    mkdirSync(entry.kind === "dir" ? path : dirname(path), { recursive: true });
    if (entry.kind === "file") writeFileSync(path, fill(entry.content));
    if (entry.kind === "symlink") symlinkSync(fill(entry.target), path);
  }
  process.chdir(base);
  const result = await discoverAndLoadExtensions(probe.configured.map(fill), fill(probe.cwd), fill(probe.agentDir));
  const strip = (text) => text.replaceAll(base, "{S}").replaceAll(`${sep}.pi${sep}`, `${sep}{CFG}${sep}`);
  results.push({ loaded: result.extensions.map((e) => strip(e.path)), failed: result.errors.map((e) => strip(e.path)) });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
