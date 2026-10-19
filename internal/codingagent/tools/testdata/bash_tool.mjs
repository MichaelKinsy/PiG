// Drive the installed Pi's bash tool over scripted commands and report the result, never a translated oracle.
import { mkdtempSync, readFileSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createBashToolDefinition, createLocalBashOperations } = await import(pathToFileURL(root + "dist/core/tools/bash.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

// Pi arms a command's timeout with setTimeout(timeoutMs) after the spawn, so what the command wrote before the kill depends on how long the
// machine took to start the shell. The timeout of the probe under test is held here instead: it never fires on its own, and a probe marked
// timeoutOnOutput fires it when the command's first output arrives. Pi's own code runs the rest: the kill, the drain, the status text.
const realSetTimeout = globalThis.setTimeout;
let probeTimeoutMs;
let armedTimeout;
globalThis.setTimeout = (callback, delay, ...rest) => {
  if (probeTimeoutMs === undefined || delay !== probeTimeoutMs) return realSetTimeout(callback, delay, ...rest);
  armedTimeout = callback;
  // A real timer far in the future gives bash.ts's clearTimeout a handle to clear.
  return realSetTimeout(() => {}, 2 ** 31 - 1);
};
const localOperations = createLocalBashOperations();

const results = [];
for (const probe of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "pi-bash-"));
  probeTimeoutMs = probe.args.timeout === undefined ? undefined : probe.args.timeout * 1000;
  armedTimeout = undefined;
  const operations = {
    exec: (command, cwd, options) => {
      let fired = false;
      return localOperations.exec(command, cwd, {
        ...options,
        onData: (data) => {
          options.onData(data);
          if (probe.timeoutOnOutput && !fired) {
            fired = true;
            armedTimeout();
          }
        },
      });
    },
  };
  const tool = createBashToolDefinition(dir, { operations });
  let outcome;
  try {
    const args = tool.prepareArguments ? tool.prepareArguments(probe.args) : probe.args;
    const result = await tool.execute("id", args, undefined, undefined, undefined);
    outcome = { text: result.content.map((c) => c.text).join(""), details: result.details ?? null };
  } catch (error) { outcome = { thrown: error.message }; }
  outcome.dir = dir;
  results.push(outcome);
}
process.stdout.write(JSON.stringify(results));
