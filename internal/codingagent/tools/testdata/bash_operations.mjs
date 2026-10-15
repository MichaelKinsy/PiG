// Run the installed Pi's createLocalBashOperations().exec (core/tools/bash.ts) over real shell commands and report, per probe, the exit code or the error, and the bytes delivered to onData.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createLocalBashOperations } = await import(pathToFileURL(root + "dist/core/tools/bash.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const operations = createLocalBashOperations();
const results = [];
for (const probe of JSON.parse(input)) {
  const controller = new AbortController();
  const chunks = [];
  if (probe.preAborted) controller.abort();
  const timer = probe.abortAfterMs ? setTimeout(() => controller.abort(), probe.abortAfterMs) : undefined;
  let out;
  try {
    const options = { onData: (data) => chunks.push(Buffer.from(data)), timeout: probe.timeout ?? undefined, env: probe.env ?? undefined };
    if (probe.useSignal) options.signal = controller.signal;
    const result = await operations.exec(probe.command, probe.cwd, options);
    out = { exitCode: result.exitCode };
  } catch (error) {
    out = { thrown: String(error && error.message) };
  }
  clearTimeout(timer);
  out.output = Buffer.concat(chunks).toString("hex");
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
