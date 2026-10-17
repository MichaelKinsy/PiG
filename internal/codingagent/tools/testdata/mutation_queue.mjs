// Run the installed Pi's withFileMutationQueue (core/tools/file-mutation-queue.ts) over scripted calls: every call is issued at once, each callback logs its start, waits for its gate, and logs its end or throws, and the gates are released in a scripted order. Reports, per release step, how many events the log holds, then the log and every call's outcome.
import { mkdirSync, readFileSync, realpathSync, symlinkSync, writeFileSync, chmodSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import fsPromises from "node:fs/promises";
import { syncBuiltinESMExports } from "node:module";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
// Path resolution is the queue's only thread-pool work. Count the realpath calls in flight, so settling waits for them instead of for a quiet period that a loaded host can outlast.
let resolving = 0;
const { realpath } = fsPromises;
fsPromises.realpath = async (...args) => {
  resolving++;
  try {
    return await realpath(...args);
  } finally {
    resolving--;
  }
};
syncBuiltinESMExports();
const { withFileMutationQueue } = await import(pathToFileURL(root + "dist/core/tools/file-mutation-queue.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
// A completed realpath settles its promise chain in the microtasks that follow its callback; the registration chain starts the next realpath there. So once a macrotask turn finds no realpath in flight, every reachable callback has run.
const settle = async () => {
  do {
    await new Promise((resolve) => setImmediate(resolve));
  } while (resolving > 0);
};
const results = [];
for (const scenario of JSON.parse(input)) {
  const dir = scenario.dir;
  process.chdir(dir);
  const log = [];
  const outcomes = {};
  const gates = scenario.calls.map(() => { let release; const promise = new Promise((resolve) => { release = resolve; }); return { promise, release }; });
  const pending = scenario.calls.map((call, i) =>
    withFileMutationQueue(call.path, async () => {
      log.push(`start ${call.name}`);
      await gates[i].promise;
      log.push(`end ${call.name}`);
      if (call.throws) throw new Error("boom " + call.name);
      return call.name;
    }).then(
      (value) => { outcomes[call.name] = `ok ${value}`; },
      (error) => { outcomes[call.name] = `err ${error.code ?? error.message}`; },
    ),
  );
  await settle();
  const counts = [log.length];
  for (const index of scenario.releases) {
    gates[index].release();
    await settle();
    counts.push(log.length);
  }
  await Promise.all(pending);
  results.push({ counts, log, outcomes });
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
