// Run the installed Pi's executeBashWithOperations (core/bash-executor.ts) over scripted BashOperations: each scenario delivers byte chunks to onData, optionally aborts the signal after one of them, and then resolves an exit code or throws. Reports every onChunk call, the result, the full-output file Pi wrote, and what was thrown.
import { existsSync, readFileSync, realpathSync, rmSync, statSync } from "node:fs";
import { pathToFileURL } from "node:url";
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
      // The stream flushes asynchronously: read once the size has stopped changing.
      let last = -1;
      for (let i = 0; i < 200; i++) {
        await new Promise((resolve) => setTimeout(resolve, 10));
        const size = existsSync(result.fullOutputPath) ? statSync(result.fullOutputPath).size : -2;
        if (size === last && size > 0) break;
        last = size;
      }
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
