// Run Pi's bedrock-converse-stream.ts isReservedHeader (String.prototype.toLowerCase) over header names from stdin, cut from the source.
// usage: node bedrock_reserved_header.mjs <bedrock-converse-stream.ts>
// stdin: a JSON array of names. Results: an array of booleans.
import { readFileSync } from "node:fs";
import module from "node:module";
function must(source, pattern) {
	const m = pattern.exec(source);
	if (!m) throw new Error(`${pattern} not found`);
	return m[0];
}
const bedrock = readFileSync(process.argv[2], "utf8");
const source = module.stripTypeScriptTypes([must(bedrock, /^const RESERVED_HEADER_EXACT = .*\n/m), must(bedrock, /^function isReservedHeader[\s\S]*?\n}\n/m)].join("\n"));
const { isReservedHeader } = new Function(`${source}\nreturn { isReservedHeader };`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((name) => isReservedHeader(name))));
