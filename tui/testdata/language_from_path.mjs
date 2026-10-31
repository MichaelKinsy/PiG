// Run the installed Pi's getLanguageFromPath (theme.ts) over seeded paths, never a translated oracle.
import "./pi-d217/register.mjs"; // D217: pin + carried Pi fixes in pi-tui utils.js
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { getLanguageFromPath } = await import(pathToFileURL(root + "dist/modes/interactive/theme/theme.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((path) => getLanguageFromPath(path) ?? null)), () => process.exit(0));
