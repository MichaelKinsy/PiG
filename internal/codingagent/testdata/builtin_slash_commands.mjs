// Drive the installed Pi's BUILTIN_SLASH_COMMANDS, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { BUILTIN_SLASH_COMMANDS } = await import(pathToFileURL(root + "dist/core/slash-commands.js"));
process.stdout.write(JSON.stringify(BUILTIN_SLASH_COMMANDS.map((c) => ({ name: c.name, description: c.description, argumentHint: c.argumentHint ?? "" }))));
