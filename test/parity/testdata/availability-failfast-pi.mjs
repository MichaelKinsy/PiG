import {pathToFileURL} from "node:url";
import {join} from "node:path";
import {readFileSync} from "node:fs";
const root = join(process.cwd(), "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
if (JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version !== "1.0.1") throw new Error("wrong Pi pin");
const {ModelRuntime} = await import(pathToFileURL(join(root, "dist/core/model-runtime.js")));
let release;
let listPending = false;
const blocked = new Promise(resolve => { release = resolve; });
const credentials = {
  read: async () => undefined,
  list: async () => { listPending = true; const result = await blocked; listPending = false; return result; },
  modify: async () => undefined,
  delete: async () => {},
};
const runtime = await ModelRuntime.create({credentials, modelsPath:null, refreshOnCreate:false});
runtime.models.getAvailable = async () => { throw new Error("availability failed"); };
let returned = false;
let error = "";
const result = runtime.getAvailable().then(() => { returned = true; }, failure => { returned = true; error = failure.message; });
// A complete event-loop turn drains the settled Promise reactions, not the deliberately unresolved credential list.
await new Promise(resolve => setImmediate(resolve));
console.log(JSON.stringify({returned, listPending, error, visibleError:runtime.getError() ?? ""}));
release([]);
await result;
