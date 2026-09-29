import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core");
const { ExtensionRunner } = await import(pathToFileURL(resolve(root, "extensions/runner.js")));
const { createExtensionRuntime, loadExtensionFromFactory } = await import(pathToFileURL(resolve(root, "extensions/loader.js")));
const { createEventBus } = await import(pathToFileURL(resolve(root, "event-bus.js")));
const { SessionManager } = await import(pathToFileURL(resolve(root, "session-manager.js")));
for (const nested of [false, true]) {
  const calls = [];
  const runtime = createExtensionRuntime();
  const bus = createEventBus();
  let runner, secondAPI, stopA, stopB;
  const emit = () => runner.emit({ type: "agent_end", messages: [] });
  const first = await loadExtensionFromFactory(pi => {
    stopA = pi.on("agent_end", async () => {
      calls.push("A");
      stopB();
      secondAPI.on("agent_end", () => { calls.push("C"); });
      if (nested) { stopA(); await emit(); }
    });
  }, process.cwd(), bus, runtime, "<inline:first>");
  const second = await loadExtensionFromFactory(pi => {
    secondAPI = pi;
    stopB = pi.on("agent_end", () => { calls.push("B"); });
  }, process.cwd(), bus, runtime, "<inline:second>");
  runner = new ExtensionRunner([first, second], runtime, process.cwd(), SessionManager.inMemory(), {});
  runner.onError(error => { throw new Error(error.error); });
  await emit();
  if (!nested) await emit();
  console.log(JSON.stringify(calls));
}
