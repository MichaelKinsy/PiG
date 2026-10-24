// Run Pi's exifOrientation (packages/durable/src/images/exif.ts, imported as-is with types stripped) over byte strings.
// stdin: a JSON array of base64 strings. stdout: a JSON array with Pi's orientation for each.
// usage: node exif_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const { exifOrientation } = await import(pathToFileURL(`${packages}durable/src/images/exif.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const encoded of JSON.parse(input)) {
	out.push(exifOrientation(Uint8Array.from(Buffer.from(encoded, "base64"))));
}
process.stdout.write(JSON.stringify(out));
