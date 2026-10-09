// Drive the installed pi-ai utils/sleep and utils/abort-signals, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const ai = root + "node_modules/@earendil-works/pi-ai/dist/utils/";
const { sleep } = await import(pathToFileURL(ai + "sleep.js"));
const { combineAbortSignals } = await import(pathToFileURL(ai + "abort-signals.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const { sleeps, combines } = JSON.parse(input);
const outcome = async (promise) => promise.then(() => "resolved", (reason) => "rejected:" + String(reason));

const sleepResults = [];
for (const test of sleeps) {
  const controller = new AbortController();
  if (test.abortBefore) controller.abort(test.abortBefore);
  const started = sleep(test.ms, controller.signal);
  if (test.abortAfterMs !== undefined) setTimeout(() => controller.abort(test.abortWith), test.abortAfterMs);
  sleepResults.push(await outcome(started));
}

const combineResults = combines.map((test) => {
  const controllers = test.signals.map((spec) => {
    if (spec === "none") return undefined;
    const controller = new AbortController();
    if (spec.startsWith("aborted:")) controller.abort(spec.slice(8));
    return controller;
  });
  const combined = combineAbortSignals(controllers.map((c) => c?.signal));
  const state = () => combined.signal === undefined ? "nosignal" : combined.signal.aborted ? "aborted:" + String(combined.signal.reason) : "live";
  const steps = [state(), String(controllers.filter(Boolean).length === 1 && combined.signal === controllers.find(Boolean).signal)];
  for (const action of test.actions ?? []) {
    if (action === "cleanup") combined.cleanup();
    else {
      const [index, reason] = action.split(":");
      controllers[Number(index)].abort(reason);
    }
    steps.push(state());
  }
  return steps;
});
process.stdout.write(JSON.stringify({ sleeps: sleepResults, combines: combineResults }));
