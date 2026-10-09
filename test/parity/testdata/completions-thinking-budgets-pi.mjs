import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.1.0") throw new Error("Expected Pi 1.1.0");
const { streamSimple } = await import(new URL("dist/compat.js", root));
const cases = JSON.parse(readFileSync(new URL("completions-thinking-budgets.json", import.meta.url), "utf8"));
for (const test of cases) {
 let captured;
 const server = createServer(async (req,res) => {
  const chunks = []; for await (const chunk of req) chunks.push(chunk);
  const body = JSON.parse(Buffer.concat(chunks).toString());
  const kwargs = body.chat_template_kwargs ?? null;
  captured = { case: test.name, budget: body.thinking_token_budget ?? null, alias: body.thinking_budget ?? null, aliasTokens: body.thinking_budget_tokens ?? null, kwargs };
  res.writeHead(200, { "content-type": "text/event-stream" }); res.end('data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\n');
 });
 await new Promise(resolve => server.listen(0,"127.0.0.1",resolve));
 try {
  const model = { id: "zai-org/glm-5.2", name: "GLM 5.2 (local vLLM)", api: "openai-completions", provider: "local-vllm", baseUrl: `http://127.0.0.1:${server.address().port}/v1`, reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 262144, maxTokens: 16384, compat: test.compat };
  const result = await streamSimple(model, { messages: [{ role: "user", content: "Hi", timestamp: 0 }] }, { apiKey: "test", reasoning: test.level, thinkingBudgets: test.budgets, maxTokens: test.maxTokens }).result();
  if (result.stopReason !== "stop") throw new Error(JSON.stringify(result));
  console.log(JSON.stringify(captured));
 } finally { await new Promise(resolve => server.close(resolve)); }
}
