// Run the pinned openai SDK's Stream.fromSSEResponse (core/streaming.mjs in ./sdk) over scripted response bodies from stdin.
// Input: [{chunks: [base64 string, ...]}]; output per case: {items: [parsed JSON value, ...], error: {name, message} | null}.
// usage: node sdk_sse_oracle.mjs
import { pathToFileURL } from "node:url";
import path from "node:path";
const sdk = path.join(import.meta.dirname, "sdk", "node_modules", "openai");
const { Stream } = await import(pathToFileURL(path.join(sdk, "core", "streaming.mjs")).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const run = async ({ chunks }) => {
	const body = new ReadableStream({
		start(controller) {
			for (const chunk of chunks) controller.enqueue(new Uint8Array(Buffer.from(chunk, "base64")));
			controller.close();
		},
	});
	const controller = new AbortController();
	const stream = Stream.fromSSEResponse(new Response(body), controller);
	const out = { items: [], error: null };
	try {
		for await (const item of stream) out.items.push(item);
	} catch (e) {
		out.error = { name: e.constructor.name, message: e.message };
	}
	return out;
};
const results = [];
for (const c of JSON.parse(input)) results.push(await run(c));
process.stdout.write(JSON.stringify(results));
