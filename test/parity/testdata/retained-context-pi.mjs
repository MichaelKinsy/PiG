import { readFileSync } from "node:fs";
import { strict as assert } from "node:assert";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core");
assert.equal(JSON.parse(readFileSync(resolve(root, "../../package.json"), "utf8")).version, "0.99.2");
const { ExtensionRunner } = await import(pathToFileURL(resolve(root, "extensions/runner.js")));
const { createExtensionRuntime } = await import(pathToFileURL(resolve(root, "extensions/loader.js")));
const { SessionManager } = await import(pathToFileURL(resolve(root, "session-manager.js")));
const runner = new ExtensionRunner([], createExtensionRuntime(), process.cwd(), SessionManager.inMemory(), {});
let text = "";
runner.setUIContext({ input: async () => text }, "tui");
let captured;
await (async ctx => { captured = ctx; })(runner.createCommandContext());
const values = [];
for (text of ["current draft", "", "replacement draft"]) values.push(await captured.ui.input("retained", ""));
runner.invalidate("generation retired");
try { await captured.ui.input("retained", ""); values.push(false); }
catch { values.push(true); }
console.log(JSON.stringify(values));
