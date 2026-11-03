// Run Pi's utf8ByteLength (packages/chord/src/output.ts, imported as-is with types stripped) over strings from stdin.
// stdin: a JSON array of strings, each spelled as its UTF-16 code units (an array of numbers), so a lone surrogate survives JSON.
// stdout: the byte length of each string.
// usage: node utf8_byte_length_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const { utf8ByteLength } = await import(pathToFileURL(`${packages}chord/src/output.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((units) => utf8ByteLength(String.fromCharCode(...units)))));
