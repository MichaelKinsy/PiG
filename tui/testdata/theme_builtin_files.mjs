// The installed Pi's built-in dark.json and light.json, byte for byte.
import { readFileSync, realpathSync } from "node:fs";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const out = {};
for (const name of ["dark", "light"]) out[name] = readFileSync(root + `dist/modes/interactive/theme/${name}.json`, "latin1");
process.stdout.write(JSON.stringify(out), () => process.exit(0));
