import { pathToFileURL } from "node:url";
const [runtimePath, scenario] = process.argv.slice(2);
const { Runtime } = await import(pathToFileURL(runtimePath).href);
const { run, runShared } = await import(pathToFileURL(scenario).href);
const runtime = new Runtime("author.mjs");
runtime.commitLoad();
const single = await run(runtime.ctx.modelRegistry, (name, config) => runtime.api.registerProvider(name, config), (name) => runtime.api.unregisterProvider(name));
// Two members of one Node cell share the cell's provider tables, as cell.mjs constructs them.
const nativeObjects = new Map(), configObjects = new Map();
const a = new Runtime("a.mjs", nativeObjects, configObjects), b = new Runtime("b.mjs", nativeObjects, configObjects);
a.commitLoad();
b.commitLoad();
const shared = await runShared(a.ctx.modelRegistry, b.ctx.modelRegistry, (name, config) => a.api.registerProvider(name, config), (name, config) => b.api.registerProvider(name, config), (name) => b.api.unregisterProvider(name));
console.log(JSON.stringify({ single, shared }));
