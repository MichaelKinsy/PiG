// Run Pi's packages/durable/src/tools/image-processor.ts (imported as-is with types stripped) over its helpers and constants.
// Op: {op: "constants"} | {op: "base64Length", n} | {op: "toBase64", length, step}  (bytes are (index * step) % 256)
// stdout: per op its result.
// usage: node image_processor_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const processor = await import(pathToFileURL(`${packages}durable/src/tools/image-processor.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	switch (op.op) {
		case "constants":
			out.push({
				limits: processor.DEFAULT_IMAGE_LIMITS,
				inline: [...processor.INLINE_IMAGE_TYPES].sort(),
				inlineHas: Object.fromEntries(op.types.map((type) => [type, processor.INLINE_IMAGE_TYPES.has(type)])),
			});
			break;
		case "base64Length":
			out.push(processor.base64Length(op.n));
			break;
		case "toBase64":
			out.push(processor.toBase64(Uint8Array.from({ length: op.length }, (_, index) => (index * op.step) % 256)));
			break;
		default:
			throw new Error(`unknown op ${op.op}`);
	}
}
process.stdout.write(JSON.stringify(out));
