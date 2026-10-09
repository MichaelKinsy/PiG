// Drive the installed Pi's truncateHead/Tail/Line/Middle and formatSize, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const t = await import(pathToFileURL(root + "dist/core/tools/truncate.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const run = ({ fn, content, maxLines, maxBytes, n }) => {
	const options = {};
	if (maxLines !== null) options.maxLines = maxLines;
	if (maxBytes !== null) options.maxBytes = maxBytes;
	switch (fn) {
		case "head": return t.truncateHead(content, options);
		case "tail": return t.truncateTail(content, options);
		case "line": return t.truncateLine(content, n);
		case "middle": return t.truncateMiddle(content, n);
		case "size": return t.formatSize(n);
	}
};
process.stdout.write(JSON.stringify(JSON.parse(input).map(run)));
