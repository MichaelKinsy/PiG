// Print the installed Pi's --help text (cli/args.ts printHelp) without color, never a translated oracle.
import { realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
process.env.FORCE_COLOR = "0";
const { printHelp } = await import(pathToFileURL(root + "dist/cli/args.js"));
printHelp([]);
