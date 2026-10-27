// Run Pi's StreamingJsonParser (packages/ai/src/utils/streaming-json.ts) over seeded documents from stdin: each probe is a list of
// pieces; after every piece the output holds the changes append() returned and JSON.stringify of snapshot() (null for undefined).
// usage: node streaming_json_parser.mjs <path to streaming-json.ts>
// Numbers JSON cannot hold are written as strings; -0 is written "-0".
import { pathToFileURL } from "node:url";
const { StreamingJsonParser } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const replacer = (_key, value) => {
	if (typeof value === "number") {
		if (Object.is(value, -0)) return "-0";
		if (!Number.isFinite(value)) return String(value);
	}
	return value;
};
const results = JSON.parse(input).map((pieces) => {
	const parser = new StreamingJsonParser();
	return pieces.map((piece) => {
		const changes = parser.append(piece);
		const snapshot = parser.snapshot();
		return { changes: JSON.stringify(changes, replacer), snapshot: snapshot === undefined ? null : JSON.stringify(snapshot, replacer), defined: snapshot !== undefined };
	});
});
process.stdout.write(JSON.stringify(results));
