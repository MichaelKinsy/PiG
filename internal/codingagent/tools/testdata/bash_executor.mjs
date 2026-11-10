// Run the installed Pi's executeBashWithOperations (core/bash-executor.ts) over scripted BashOperations: each scenario delivers byte chunks to onData, optionally aborts the signal after one of them, and then resolves an exit code or throws. Reports every onChunk call, the result, the full-output file Pi wrote (read after Pi's stream closed), and what was thrown.
import fs, { existsSync, readFileSync, realpathSync, rmSync } from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL } from "node:url";
// Pi's executor ends its output stream without awaiting it, so the file is complete only once the stream has closed. Record the streams Pi opens to wait for that, instead of guessing from the file's size.
const outputStreams = [];
const createWriteStream = fs.createWriteStream;
fs.createWriteStream = (...args) => {
  const stream = createWriteStream.apply(fs, args);
  outputStreams.push(stream);
  return stream;
};
syncBuiltinESMExports();
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { executeBashWithOperations } = await import(pathToFileURL(root + "dist/core/bash-executor.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const scenario of JSON.parse(input)) {
  const controller = new AbortController();
  const chunks = [];
  outputStreams.length = 0;
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
  let out;
  try {
    const result = await executeBashWithOperations("cmd", "/cwd", operations, { onChunk: (c) => chunks.push(c), signal: scenario.useSignal ? controller.signal : undefined });
    let file = null;
    if (result.fullOutputPath !== undefined) {
      for (const stream of outputStreams) await new Promise((resolve) => (stream.closed ? resolve() : (stream.once("close", resolve), stream.once("error", resolve))));
      file = existsSync(result.fullOutputPath) ? readFileSync(result.fullOutputPath, "utf8") : "<missing>";
      rmSync(result.fullOutputPath, { force: true });
    }
    out = { chunks, output: result.output, exitCode: result.exitCode ?? null, cancelled: result.cancelled, truncated: result.truncated, file };
  } catch (error) {
    out = { chunks, thrown: String(error && error.message) };
  }
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
