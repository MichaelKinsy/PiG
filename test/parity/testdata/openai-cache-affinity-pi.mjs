import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.2") throw new Error("Expected Pi 1.0.2");
const { stream: completions } = await import(new URL("dist/api/openai-completions.js", root));
const { stream: responses } = await import(new URL("dist/api/openai-responses.js", root));
const { getModel, normalizeContext } = await import(new URL("dist/compat.js", root));
const cases = JSON.parse(readFileSync(new URL("openai-cache-affinity.json", import.meta.url), "utf8"));
for (const [api, stream, id] of [["completions", completions, "gpt-4o-mini"], ["responses", responses, "gpt-5-mini"]]) {
 for (const test of cases) {
  let captured;
  const server = createServer(async (req, res) => {
   const chunks = []; for await (const chunk of req) chunks.push(chunk);
   const body = JSON.parse(Buffer.concat(chunks).toString());
   captured = { api, case: test.name, key: body.prompt_cache_key ?? "", retention: body.prompt_cache_retention ?? "", session: req.headers.session_id ?? "", client: req.headers["x-client-request-id"] ?? "", affinity: req.headers["x-session-affinity"] ?? "", router: req.headers["x-session-id"] ?? "", reasoning: body.reasoning ?? null, choice: body.tool_choice ?? "" };
   res.writeHead(200, { "content-type": "text/event-stream" });
   res.end(api === "responses" ? 'data: {"type":"response.completed","response":{"status":"completed"}}\n\n' : 'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\n');
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  try {
   const model = { ...getModel("openai", id), api: `openai-${api}`, baseUrl: `http://127.0.0.1:${server.address().port}${test.path}`, compat: test.compat, headers: test.modelHeaders };
   const context = { systemPrompt: "sys", messages: [{ role: "user", content: "hi", timestamp: 0 }] };
   if (api === "responses") context.tools = [{ name: "ping", description: "Ping", parameters: { type: "object", properties: { value: { type: "string" } }, required: ["value"] } }];
   const result = await stream(model, normalizeContext(context), { apiKey: "test-key", sessionId: test.session, cacheRetention: test.retention, headers: test.headers, env: { PI_CACHE_RETENTION: "" }, ...(api === "responses" ? { toolChoice: "required" } : {}) }).result();
   if (result.stopReason !== "stop") throw new Error(JSON.stringify(result));
   console.log(JSON.stringify(captured));
  } finally { await new Promise(resolve => server.close(resolve)); }
 }
}
