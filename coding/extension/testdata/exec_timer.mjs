// Run the installed Pi's execCommand (core/exec.ts) on one scenario read from stdin and report, on one monotonic clock, how long its spawn call took and how long the call took to resolve after spawn returned: the timeout starts when spawn returns (exec.ts:75), and the two durations tell a timeout that started before the program was found from one that started after.
import { readFileSync, realpathSync } from "node:fs";
import childProcess from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const spawn = childProcess.spawn;
let called, returned;
childProcess.spawn = (...args) => {
  called = performance.now();
  try {
    return spawn(...args);
  } finally {
    returned = performance.now();
  }
};
syncBuiltinESMExports();
const { execCommand } = await import(pathToFileURL(root + "dist/core/exec.js"));
const s = JSON.parse(readFileSync(0, "utf8"));
const r = await execCommand(s.command, s.args, s.cwd, { timeout: s.timeout });
const resolved = performance.now();
const report = { stdout: r.stdout, stderr: r.stderr, code: r.code, killed: r.killed, searchMs: returned - called, afterSpawnMs: resolved - returned };
process.stdout.write(JSON.stringify(report), () => process.exit(0));
