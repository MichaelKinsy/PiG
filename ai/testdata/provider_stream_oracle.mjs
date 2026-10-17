// Run one of Pi's API providers (the installed pi-ai dist) over scripted response bodies served through a replaced global fetch.
// Input: [{chunks: [base64, ...]}]; output per case: {events: [type, ...], message: {content, stopReason, usage, errorMessage}}.
// usage: node provider_stream_oracle.mjs <Pi version> <api file, e.g. google-generative-ai> <export, e.g. googleGenerativeAIApi> <model api> <model provider>
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const [, , , apiFile, exportName, modelApi, modelProvider] = process.argv;
const api = (await import(pathToFileURL(dist + `api/${apiFile}.lazy.js`)))[exportName];
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const run = async ({ chunks, status, statusText, drop, attempts: scripted, maxRetries = 0 }) => {
	const attempts = scripted ?? [];
	let requests = 0;
	globalThis.fetch = async () => {
		const attempt = attempts[requests++];
		if (attempt) return new Response(attempt.body, { status: attempt.status, statusText: attempt.statusText, headers: { "content-type": "application/json", ...attempt.headers } });
		// One chunk per pull, so a broken body errors after the consumer has read every chunk (error() discards queued chunks).
		let next = 0;
		const body = new ReadableStream({
			pull(controller) {
				if (next < chunks.length) controller.enqueue(new Uint8Array(Buffer.from(chunks[next++], "base64")));
				else if (drop) controller.error(new TypeError("terminated"));
				else controller.close();
			},
		});
		return new Response(body, { status, statusText: status === 200 ? "OK" : statusText, headers: { "content-type": status === 200 ? "text/event-stream" : "application/json" } });
	};
	const model = { id: "m", name: "m", api: modelApi, provider: modelProvider, baseUrl: "https://h.test/v1", reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000000, maxTokens: 8192 };
	const events = [];
	const stream = api().stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: process.env.PI_ORACLE_API_KEY ?? "k", maxRetries, ...(process.env.PI_ORACLE_TRANSPORT ? { transport: process.env.PI_ORACLE_TRANSPORT } : {}) });
	for await (const event of stream) events.push(event.type);
	const m = await stream.result();
	return { requests, events, message: { content: m.content, stopReason: m.stopReason, usage: m.usage, errorMessage: m.errorMessage ?? null, responseId: m.responseId ?? null } };
};
const results = [];
for (const c of JSON.parse(input)) results.push(await run(c));
process.stdout.write(JSON.stringify(results));
