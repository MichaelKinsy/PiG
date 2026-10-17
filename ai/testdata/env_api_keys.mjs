// Drive the installed pi-ai's getEnvApiKey and findEnvKeys over scripted provider environments, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { getEnvApiKey, findEnvKeys } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/env-api-keys.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map(({ provider, env }) => ({ key: getEnvApiKey(provider, env) ?? null, found: findEnvKeys(provider, env) ?? null }))));
