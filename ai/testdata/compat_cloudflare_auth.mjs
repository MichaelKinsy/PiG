// Drives the installed pi-ai compat stream and streamSimple (compat.ts) for a cloudflare- provider model with no credential, with only the AI Gateway
// authorization header, and with an API key, against a local server: it records how many requests reached the server and the settled result.
import { createServer } from "node:http";
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const compat = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/compat.js"));
let requests = 0;
const server = createServer((req, res) => {
	req.resume();
	req.on("end", () => {
		requests++;
		res.writeHead(400, { "content-type": "application/json" });
		res.end('{"error":{"message":"fixture"}}');
	});
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const model = { id: "m1", name: "m1", api: "openai-completions", provider: process.argv[3], baseUrl: `http://127.0.0.1:${server.address().port}/v1`, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const ctx = { messages: [{ role: "user", content: "hi", timestamp: 1 }] };
const cases = [["no credential", {}], ["gateway header", { headers: { "cf-aig-authorization": "Bearer g" } }], ["api key", { apiKey: "k" }]];
const out = [];
for (const [name, options] of cases) {
	for (const fn of ["stream", "streamSimple"]) {
		requests = 0;
		let r;
		try {
			r = await compat[fn](model, ctx, { ...options, env: {} }).result();
		} catch (e) {
			r = { stopReason: "threw", errorMessage: String(e && e.message) };
		}
		out.push({ name, fn, requests, stopReason: r.stopReason, errorMessage: r.errorMessage });
	}
}
server.close();
process.stdout.write(JSON.stringify(out));
