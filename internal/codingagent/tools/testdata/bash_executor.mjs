// Run the installed Pi's executeBashWithOperations (core/bash-executor.ts) over scripted BashOperations: each scenario delivers byte chunks to onData, optionally aborts the signal after one of them, and then resolves an exit code or throws. Reports every onChunk call, the result, the full-output file Pi wrote, and what was thrown.
import fs, { existsSync, readFileSync, realpathSync, rmSync } from "node:fs";
import { once } from "node:events";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
// Pi writes its spill file through an fs.WriteStream it never hands back and ends without waiting for it: record the streams, so the file is read only once Pi's stream has closed. A size that stops changing is no proof of that: libuv on Windows writes the buffers of one writev one WriteFile at a time, and a write thread that is descheduled between two of them leaves a prefix standing for longer than any poll interval.
const spills = [];
const createWriteStream = fs.createWriteStream;
fs.createWriteStream = (...args) => {
  const stream = createWriteStream(...args);
  spills.push(stream);
  return stream;
};
syncBuiltinESMExports();
const { executeBashWithOperations } = await import(pathToFileURL(root + "dist/core/bash-executor.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const scenario of JSON.parse(input)) {
  const controller = new AbortController();
  const chunks = [];
  const operations = {
    exec: async (command, cwd, options) => {
      if (scenario.abortAfter === -1) controller.abort();
      for (let i = 0; i < scenario.chunks.length; i++) {
        options.onData(Buffer.from(scenario.chunks[i], "hex"));
        if (scenario.abortAfter === i) controller.abort();
      }
      if (scenario.error !== null) throw new Error(scenario.error);
      return { exitCode: scenario.exitCode };
    },
  };
  let result;
  let failure;
  try {
    result = await executeBashWithOperations("cmd", "/cwd", operations, { onChunk: (c) => chunks.push(c), signal: scenario.useSignal ? controller.signal : undefined });
  } catch (error) {
    failure = { error };
  }
  // Pi ends its spill stream without waiting for it: the file is complete only once the stream has closed.
  await Promise.all(spills.splice(0).map((stream) => (stream.closed ? undefined : once(stream, "close"))));
  let out;
  if (failure) {
    out = { chunks, thrown: String(failure.error && failure.error.message) };
  } else {
    let file = null;
    if (result.fullOutputPath !== undefined) {
      file = existsSync(result.fullOutputPath) ? readFileSync(result.fullOutputPath, "utf8") : "<missing>";
      rmSync(result.fullOutputPath, { force: true });
    }
    out = { chunks, output: result.output, exitCode: result.exitCode ?? null, cancelled: result.cancelled, truncated: result.truncated, file };
  }
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
