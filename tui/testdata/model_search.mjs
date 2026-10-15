// Drive the installed Pi's getModelSearchText and getModelSelectorSearchText, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { getModelSearchText, getModelSelectorSearchText } = await import(pathToFileURL(root + "dist/modes/interactive/model-search.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((item) => {
	const { name, ...rest } = item;
	const pi = name === null ? rest : { ...rest, name };
	return [getModelSearchText(pi), getModelSelectorSearchText(pi)];
})));
