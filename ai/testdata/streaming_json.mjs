// Drive the installed pi-ai json-parse.js (repairJson, parseJsonWithRepair, parseStreamingJson) over texts from stdin:
// [string] -> [{repair, parse, stream, text}], where parse is {ok} or {err: true}, non-finite numbers are written as {"$num": "Infinity"}, and text is
// JSON.stringify of the streaming result: its member order and number spelling.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { repairJson, parseJsonWithRepair, parseStreamingJson } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/utils/json-parse.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const encode = (value) =>
	JSON.parse(
		JSON.stringify(value, (key, v) => {
			if (typeof v === "number" && !Number.isFinite(v)) return { $num: String(v) };
			if (v === undefined) return { $undefined: true };
			return v;
		}),
	);
const results = JSON.parse(input).map((text) => {
	let parse;
	try {
		parse = { ok: encode(parseJsonWithRepair(text)) };
	} catch {
		parse = { err: true };
	}
	const stream = parseStreamingJson(text);
	return { repair: repairJson(text), parse, stream: encode(stream), text: JSON.stringify(stream) ?? null };
});
process.stdout.write(JSON.stringify(results));
