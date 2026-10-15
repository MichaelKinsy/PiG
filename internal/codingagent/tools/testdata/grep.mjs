// Drive the installed Pi's grep tool over a directory, never a translated oracle: argv[2] is the Pi version, argv[3] the directory; stdin is a JSON list of parameter objects; stdout is each result's text and details.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createGrepTool } = await import(pathToFileURL(root + "dist/core/tools/grep.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const tool = createGrepTool(process.argv[3]);
const out = [];
for (const params of JSON.parse(input)) {
	const result = await tool.execute("oracle", params);
	out.push({ text: result.content.map((block) => block.text).join(""), details: result.details ?? null });
}
process.stdout.write(JSON.stringify(out));
