import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core");
const { ExtensionRunner } = await import(pathToFileURL(resolve(root, "extensions/runner.js")));
const { createExtensionRuntime } = await import(pathToFileURL(resolve(root, "extensions/loader.js")));
const { SessionManager } = await import(pathToFileURL(resolve(root, "session-manager.js")));
const runner = new ExtensionRunner([], createExtensionRuntime(), process.cwd(), SessionManager.inMemory(), {});
const cold = runner.createContext();
console.log(JSON.stringify(["default", cold.scopedModels]));
let scoped = [{ model: { id: "scoped-test" }, thinkingLevel: "high" }];
runner.bindCore({}, { getScopedModels: () => scoped });
const event = runner.createContext(), command = runner.createCommandContext();
console.log(JSON.stringify(["bound", event.scopedModels, event.scopedModels === scoped, command.scopedModels === scoped, cold.scopedModels]));
scoped = [{ model: { id: "second" } }];
console.log(JSON.stringify(["replaced", event.scopedModels, event.scopedModels === scoped, command.scopedModels === scoped]));
runner.bindCore({}, { getScopedModels: () => [] });
console.log(JSON.stringify(["captured", event.scopedModels, runner.createContext().scopedModels]));
runner.invalidate("replaced");
for (const ctx of [cold, event, command]) {
 try { console.log(JSON.stringify(ctx.scopedModels)); } catch (error) { console.log(JSON.stringify(error.message)); }
}
