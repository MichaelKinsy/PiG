import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import factory from "../scenarios/extensions-runtime/testdata/ext/user-bash-validation.mjs";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core");
const { ExtensionRunner } = await import(pathToFileURL(resolve(root, "extensions/runner.js")));
const { createExtensionRuntime, loadExtensionFromFactory } = await import(pathToFileURL(resolve(root, "extensions/loader.js")));
const { createEventBus } = await import(pathToFileURL(resolve(root, "event-bus.js")));
const { SessionManager } = await import(pathToFileURL(resolve(root, "session-manager.js")));
const runtime = createExtensionRuntime();
const extension = await loadExtensionFromFactory(factory, process.cwd(), createEventBus(), runtime);
const runner = new ExtensionRunner([extension], runtime, process.cwd(), SessionManager.inMemory(), {});
let reported;
runner.onError(error => reported.push([error.event, error.error]));
for (const command of ["valid", "undefined-exit", "missing-exit", "null-exit", "null-path", "both-with-null-operations", "empty", "incomplete", "throws", "observe"]) {
  reported = [];
  let result = null, failure = "";
  try { result = await runner.emitUserBash({ type: "user_bash", command, cwd: process.cwd(), excludeFromContext: false }) ?? null; }
  catch (error) { failure = error.message; }
  if (command === "undefined-exit" && result?.result) {
    console.log(JSON.stringify([command, Object.hasOwn(result.result, "exitCode"), result.result.exitCode, failure, reported]));
  } else {
    console.log(JSON.stringify([command, result, failure, reported]));
  }
}
