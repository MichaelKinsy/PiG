// Prints JSON.stringify of every model Pi 1.2.0's ModelRuntime holds offline with no models.json, one per line: the objects an
// extension receives as ctx.model and from ctx.modelRegistry. Run: node model-object-oracle.mjs <pi-coding-agent package root>
// with PI_OFFLINE set and a scratch HOME.
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.argv[2];
const { ModelRuntime } = await import(pathToFileURL(join(root, "dist/core/model-runtime.js")).href);
const runtime = await ModelRuntime.create({ modelsPath: null, authPath: join(process.env.HOME, "auth.json") });
for (const model of runtime.getModels()) process.stdout.write(`${JSON.stringify(model)}\n`);
