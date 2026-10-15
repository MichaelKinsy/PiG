// Drive the installed pi-ai radiusProvider, never a translated oracle: radiusProvider() against radiusProvider({}).
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { radiusProvider } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/providers/radius.js"));
const describe = (provider) => ({ id: provider.id, name: provider.name, models: provider.getModels ? provider.getModels().length : provider.models?.length ?? null });
process.stdout.write(JSON.stringify({ bare: describe(radiusProvider()), empty: describe(radiusProvider({})), custom: describe(radiusProvider({ id: "gw", name: "Gateway" })) }));
