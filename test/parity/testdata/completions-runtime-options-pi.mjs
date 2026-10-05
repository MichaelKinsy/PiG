import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.3") throw new Error("Expected Pi 1.0.3");
const { getModel, streamSimple } = await import(new URL("dist/compat.js", root));
Object.assign(process.env, { CLOUDFLARE_API_KEY: "cf-token", CLOUDFLARE_ACCOUNT_ID: "account-id", CLOUDFLARE_GATEWAY_ID: "gateway-id" });
const cases = JSON.parse(readFileSync(new URL("completions-runtime-options.json", import.meta.url), "utf8"));
for (const test of cases) {
 let captured;
 const server = createServer(async (req, res) => {
  const chunks = []; for await (const chunk of req) chunks.push(chunk);
  const body = JSON.parse(Buffer.concat(chunks).toString());
  captured = { case: test.name, maxCompletion: body.max_completion_tokens ?? null, maxTokens: body.max_tokens ?? null, maxOutput: body.max_output_tokens ?? null, tools: body.tools ?? null, path: req.url, auth: req.headers.authorization ?? "", gatewayAuth: req.headers["cf-aig-authorization"] ?? "", affinity: req.headers["x-session-affinity"] ?? "" };
  res.writeHead(200, { "content-type": "text/event-stream" });
  res.end(req.url.endsWith("/responses") ? 'data: {"type":"response.completed","response":{"status":"completed"}}\n\n' : 'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\n');
 });
 await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
 try {
  const model = { ...getModel(test.provider, test.model) };
  model.baseUrl = `http://127.0.0.1:${server.address().port}${new URL(model.baseUrl).pathname}`;
  // URL.pathname encodes the placeholders; retain the provider's literal template.
  model.baseUrl = model.baseUrl.replaceAll("%7B", "{").replaceAll("%7D", "}");
  if (test.provider === "openai") { model.api = "openai-completions"; delete model.compat; }
  if (test.window) model.contextWindow = test.window;
  if (test.cap) model.maxTokens = test.cap;
  const context = { messages: [{ role: "user", content: test.length ? "x".repeat(test.length) : "hi", timestamp: 0 }] };
  if (test.history) {
   const cost = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 };
   context.tools = [];
   context.messages = [{ role: "user", content: "use the tool", timestamp: 0 }, { role: "assistant", api: "openai-completions", provider: "openai", model: "gpt-4o-mini", stopReason: "toolUse", content: [{ type: "toolCall", id: "t1", name: "noop", arguments: {} }], usage: { ...cost, totalTokens: 0, cost }, timestamp: 0 }, { role: "toolResult", toolCallId: "t1", toolName: "noop", content: [{ type: "text", text: "done" }], isError: false, timestamp: 0 }];
  }
  const result = await streamSimple(model, context, { ...(test.provider === "openai" ? { apiKey: "test" } : {}), maxTokens: test.maxTokens, reasoning: test.thinking, sessionId: test.session, headers: test.headers }).result();
  if (result.stopReason !== "stop") throw new Error(JSON.stringify(result));
  console.log(JSON.stringify(captured));
 } finally { await new Promise(resolve => server.close(resolve)); }
}
