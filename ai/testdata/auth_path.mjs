// Create the installed Pi's AuthStorage over each spelling of an auth.json path (absolute, relative, ~, file:// URL, dot segments) and write one credential through it: reports the files the sandbox holds afterwards and the credential read back. Usage: node auth_path.mjs <pi version> <sandbox>; probes arrive on stdin with {S} for the sandbox.
import { mkdirSync, readFileSync, readdirSync, realpathSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const sandbox = process.argv[3];
const { AuthStorage } = await import(pathToFileURL(root + "dist/core/auth-storage.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const tree = (dir) => readdirSync(dir).sort().flatMap((name) => {
  const full = join(dir, name);
  return statSync(full).isDirectory() ? tree(full) : [relative(sandbox, full)];
});
const results = [];
for (const [index, probe] of JSON.parse(input).entries()) {
  const base = join(sandbox, `p${index}`);
  mkdirSync(join(base, "home"), { recursive: true });
  // os.homedir() reads USERPROFILE on Windows and HOME elsewhere.
  process.env.HOME = process.env.USERPROFILE = join(base, "home");
  process.chdir(base);
  let outcome;
  try {
    const storage = AuthStorage.create(probe.replaceAll("{S}", base));
    await storage.modify("openai", () => ({ type: "api_key", key: "sk-1" }));
    outcome = JSON.stringify((await storage.read("openai")) ?? null);
  } catch (error) {
    outcome = `error ${error.message.replaceAll(base, "{S}")}`;
  }
  results.push({ outcome, files: tree(base).map((f) => f.replace(/^p\d+[\\/]/, "")).filter((f) => !/\.lock($|[\\/])/.test(f)) });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
