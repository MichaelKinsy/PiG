// Drive the installed pi-tui findWordBackward / findWordForward (word-navigation.ts) over probes from stdin: [{text, cursors}] -> [{back, fwd}].
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { findWordBackward, findWordForward } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/word-navigation.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => ({
	back: probe.cursors.map((cursor) => findWordBackward(probe.text, cursor)),
	fwd: probe.cursors.map((cursor) => findWordForward(probe.text, cursor)),
}));
process.stdout.write(JSON.stringify(results));
