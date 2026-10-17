// Run the installed Pi's execCommand (core/exec.ts) on seeded real commands, working directories, timeouts and pre-aborted signals: output, exit code (a signal-killed child resolves as 0), the killed flag, a program or directory that cannot start (code 1) and arguments Node's spawn rejects.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { execCommand } = await import(pathToFileURL(root + "dist/core/exec.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const s of JSON.parse(input)) {
  const controller = new AbortController();
  if (s.aborted) controller.abort();
  const options = { ...(s.timeout !== undefined ? { timeout: s.timeout } : {}), ...(s.cwdOption ? { cwd: s.cwdOption } : {}), signal: controller.signal };
  try {
    const r = await execCommand(s.command, s.args, s.cwd, options);
    results.push({ stdout: r.stdout, stderr: r.stderr, code: r.code, killed: r.killed });
  } catch (error) { results.push({ thrown: String(error && error.code) + ":" + String(error && error.message).slice(0, 120) }); }
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
