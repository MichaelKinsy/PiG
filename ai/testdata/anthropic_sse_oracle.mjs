// Run Pi's iterateAnthropicEvents (the installed pi-ai dist, api/anthropic-messages.js, with its private generator exported by a load hook) over scripted response bodies from stdin.
// Input: [{chunks: [base64, ...]}]; output per case: {events: [parsed event, ...], error: message | null}.
// usage: node anthropic_sse_oracle.mjs <Pi version>
import { readFileSync, realpathSync } from "node:fs";
import module from "node:module";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const file = root + "node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.js";
module.registerHooks({
	load(url, context, next) {
		const result = next(url, context);
		if (url.endsWith("/dist/api/anthropic-messages.js")) {
			return { ...result, source: `${result.source}\nexport { iterateAnthropicEvents };\n`, shortCircuit: true };
		}
		return result;
	},
});
const { iterateAnthropicEvents } = await import(pathToFileURL(file).href);
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
	const out = { events: [], error: null };
	try {
		for await (const event of iterateAnthropicEvents(new Response(body))) out.events.push(event);
	} catch (e) {
		out.error = e?.message ?? String(e);
	}
	return out;
};
const results = [];
for (const c of JSON.parse(input)) results.push(await run(c));
process.stdout.write(JSON.stringify(results));
