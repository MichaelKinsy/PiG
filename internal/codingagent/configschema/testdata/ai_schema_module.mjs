// Print the exports of a Pi ai/providers schema module (argv[3]: model-schema or compat-schema) as JSON: {name: JSON.stringify(value, null, 2)}.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const piCodingAgent = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(piCodingAgent + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const module = await import(pathToFileURL(piCodingAgent + "node_modules/@earendil-works/pi-ai/dist/providers/" + process.argv[3] + ".js"));
const out = {};
for (const [name, value] of Object.entries(module)) out[name] = JSON.stringify(value, null, 2);
process.stdout.write(JSON.stringify(out), () => process.exit(0));
