// Runs a model registry program through Pi 1.2.0's ModelRuntime, the runtime behind ctx.modelRegistry (model-registry.ts:72
// find is runtime.getModel), and prints one JSON array: for each "find" step, an object mapping each model id to JSON.stringify
// of the model object an extension receives (null when Pi has none).
// Run: node model-registry-oracle.mjs <pi-coding-agent package root> <program JSON> with PI_OFFLINE set and a scratch HOME.
// A program is a list of steps: {"modelsJson": text} writes models.json (the first step before the runtime starts, a later
// one followed by refresh()); {"register": [providerId, config]}; {"unregister": providerId}; {"find": [providerId, ...ids]}.
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [root, programText] = process.argv.slice(2);
const program = JSON.parse(programText);
const { ModelRuntime } = await import(pathToFileURL(join(root, "dist/core/model-runtime.js")).href);
const modelsPath = join(process.env.HOME, "models.json");
let runtime;
const finds = [];
for (const step of program) {
  if (step.modelsJson !== undefined) {
    writeFileSync(modelsPath, step.modelsJson);
    if (runtime) await runtime.refresh();
    continue;
  }
  runtime ??= await ModelRuntime.create({ modelsPath, authPath: join(process.env.HOME, "auth.json") });
  if (step.register) runtime.registerProvider(...step.register);
  else if (step.unregister) runtime.unregisterProvider(step.unregister);
  else if (step.find) {
    const [provider, ...ids] = step.find;
    const objects = {};
    for (const id of ids) {
      const model = runtime.getModel(provider, id);
      objects[id] = model === undefined ? null : JSON.stringify(model);
    }
    finds.push(objects);
  }
}
process.stdout.write(JSON.stringify(finds));
