// Run the installed Pi's getPathCommandArgument (interactive-mode.ts, /export and /import), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "0";
const { InteractiveMode } = await import(pathToFileURL(root + "dist/modes/interactive/interactive-mode.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map(({ text, command }) => InteractiveMode.prototype.getPathCommandArgument.call({}, text, command) ?? null);
process.stdout.write(JSON.stringify(results), () => process.exit(0));
