// Runs the real packages/durable/src/env/decode.ts StreamDecoder from the pinned upstream mirror over the chunkings on stdin:
// [{ chunks: [hex, ...] }] -> [text, ...] where each text is every decode(chunk) result followed by the final decode().
import { readFileSync } from "node:fs";
const { StreamDecoder } = await import(process.argv[2] + "/.upstream/current/packages/durable/src/env/decode.ts");
const cases = JSON.parse(readFileSync(0, "utf8"));
const out = cases.map(({ chunks }) => {
	const decoder = new StreamDecoder();
	let text = "";
	for (const chunk of chunks) text += decoder.decode(Buffer.from(chunk, "hex"));
	return text + decoder.decode();
});
process.stdout.write(JSON.stringify(out));
