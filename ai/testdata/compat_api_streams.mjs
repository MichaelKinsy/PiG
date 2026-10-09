// Drives the installed pi-ai compat API objects (azureOpenAIResponsesApi, googleGenerativeAIApi, mistralConversationsApi,
// and the no-credential setup failures of azureOpenAIResponsesApi, googleGenerativeAIApi, googleVertexApi and mistralConversationsApi) against a local server, so the Go side is compared with what Pi sends and returns.
import { createServer } from "node:http";
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const compat = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/compat.js"));

const sse = (events) => events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join("");
const responses = {
	azureOpenAIResponsesApi: sse([
		{ type: "response.created", response: { id: "resp_1" } },
		{ type: "response.output_item.added", item: { type: "message", id: "msg_1", role: "assistant", status: "in_progress", content: [] } },
		{ type: "response.content_part.added", part: { type: "output_text", text: "" } },
		{ type: "response.output_text.delta", delta: "Hello" },
		{ type: "response.output_text.delta", delta: " there" },
		{ type: "response.output_item.done", item: { type: "message", id: "msg_1", role: "assistant", status: "completed", content: [{ type: "output_text", text: "Hello there", annotations: [] }] } },
		{ type: "response.completed", response: { id: "resp_1", status: "completed", usage: { input_tokens: 5, output_tokens: 2, total_tokens: 7, input_tokens_details: { cached_tokens: 0 } } } },
	]),
	googleGenerativeAIApi: sse([
		{ candidates: [{ content: { role: "model", parts: [{ text: "Hello" }] } }] },
		{ candidates: [{ content: { role: "model", parts: [{ text: " there" }] }, finishReason: "STOP" }], usageMetadata: { promptTokenCount: 5, candidatesTokenCount: 2, totalTokenCount: 7 } },
	]),
	mistralConversationsApi: sse([
		{ id: "c1", model: "m1", choices: [{ index: 0, delta: { role: "assistant", content: "Hello" }, finish_reason: null }] },
		{ id: "c1", model: "m1", choices: [{ index: 0, delta: { content: " there" }, finish_reason: "stop" }], usage: { prompt_tokens: 5, completion_tokens: 2, total_tokens: 7 } },
	]) + "data: [DONE]\n\n",
};
const cases = {
	azureOpenAIResponsesApi: { api: "azure-openai-responses", provider: "azure-openai-responses", path: "/openai/v1" },
	googleGenerativeAIApi: { api: "google-generative-ai", provider: "google", path: "/v1beta" },
	mistralConversationsApi: { api: "mistral-conversations", provider: "mistral", path: "" },
};
const out = { responses, served: {}, setup: {} };
for (const [fn, c] of Object.entries(cases)) {
	let seen = null;
	const server = createServer((req, res) => {
		let body = "";
		req.on("data", (d) => (body += d));
		req.on("end", () => {
			seen = { method: req.method, url: req.url, headers: { "x-goog-api-key": req.headers["x-goog-api-key"], authorization: req.headers.authorization, "api-key": req.headers["api-key"] }, body: JSON.parse(body) };
			res.writeHead(200, { "content-type": "text/event-stream" });
			res.end(responses[fn]);
		});
	});
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "m1", name: "m1", api: c.api, provider: c.provider, baseUrl: `http://127.0.0.1:${server.address().port}${c.path}`, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
	const ctx = { messages: [{ role: "user", content: "hi", timestamp: 1 }] };
	const result = await compat[fn]().stream(model, ctx, { apiKey: "k" }).result();
	const simple = seen;
	seen = null;
	const simpleResult = await compat[fn]().streamSimple(model, ctx, { apiKey: "k" }).result();
	server.close();
	const brief = (r) => ({ stopReason: r.stopReason, errorMessage: r.errorMessage, text: r.content.map((p) => p.text).join(""), input: r.usage.input, output: r.usage.output, api: r.api, provider: r.provider, model: r.model });
	out.served[fn] = { stream: { request: simple, result: brief(result) }, streamSimple: { request: seen, result: brief(simpleResult) } };
}
const noKey = [["azureOpenAIResponsesApi", "azure-openai-responses", "azure-openai-responses", "https://x.openai.azure.com/openai/v1"], ["googleGenerativeAIApi", "google-generative-ai", "google", "https://generativelanguage.googleapis.com/v1beta"], ["googleVertexApi", "google-vertex", "google-vertex", "https://{location}-aiplatform.googleapis.com"], ["mistralConversationsApi", "mistral-conversations", "mistral", "https://api.mistral.ai"]];
for (const [fn, api, provider, baseUrl] of noKey) {
	const model = { id: "m1", name: "m1", api, provider, baseUrl, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
	const r = await compat[fn]().stream(model, { messages: [{ role: "user", content: "hi", timestamp: 1 }] }, {}).result();
	out.setup[fn] = { stopReason: r.stopReason, errorMessage: r.errorMessage, api: r.api, provider: r.provider, model: r.model };
}
process.stdout.write(JSON.stringify(out, null, 1));
