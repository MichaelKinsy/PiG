// Run getStandardBedrockEndpointRegion (private, cut from Pi's packages/ai/src/api/bedrock-converse-stream.ts) over base URLs from stdin; the result is the region or null.
// usage: node bedrock_region.mjs <path to bedrock-converse-stream.ts>
import { readFileSync } from "node:fs";
import module from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = /^function getStandardBedrockEndpointRegion[\s\S]*?\n}\n/m.exec(source);
if (!cut) throw new Error("getStandardBedrockEndpointRegion not found");
const region = new Function(`${module.stripTypeScriptTypes(cut[0])}; return getStandardBedrockEndpointRegion;`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((baseUrl) => region(baseUrl === "" ? undefined : baseUrl) ?? null)));
