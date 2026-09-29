import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core");
const { ExtensionRunner } = await import(pathToFileURL(resolve(root, "extensions/runner.js")));
const { createExtensionRuntime } = await import(pathToFileURL(resolve(root, "extensions/loader.js")));
const { SessionManager } = await import(pathToFileURL(resolve(root, "session-manager.js")));
const runner = new ExtensionRunner([], createExtensionRuntime(), process.cwd(), SessionManager.inMemory(), {});
const event = runner.createContext(), command = runner.createCommandContext();
function record(label) {
  console.log(JSON.stringify([label, ...[event, command].map(ctx => [ctx.mode, ctx.hasUI, ctx.ui === runner.getUIContext()])]));
}
record("initial");
runner.setUIContext({}); record("default");
for (const mode of ["rpc", "tui", "json", "print"]) {
  runner.setUIContext({}, mode); record(mode);
  runner.setUIContext(undefined, mode); record(`${mode}:clear`);
}
runner.setUIContext(runner.getUIContext()); record("supplied-noop");
runner.invalidate("replaced");
console.log(JSON.stringify([event, command].map(ctx => ["mode", "hasUI", "ui"].map(key => {
  try { return ctx[key]; } catch (error) { return error.message; }
}))));
