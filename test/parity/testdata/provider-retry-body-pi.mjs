import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.3") throw new Error("Expected Pi 1.0.3");
const { stream } = await import(new URL("dist/api/openai-completions.js", root));
const { normalizeContext } = await import(new URL("dist/utils/transcript.js", root));
let attempts = 0;
const server = createServer((_req,res) => { attempts++; res.writeHead(429, { "retry-after": "277403" }); res.end("rate limited"); });
await new Promise(resolve => server.listen(0,"127.0.0.1",resolve));
try {
 const model = { id: "test-model", name: "Test Model", api: "openai-completions", provider: "opencode-go", baseUrl: `http://127.0.0.1:${server.address().port}`, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
 const result = await stream(model, normalizeContext({ systemPrompt: "", messages: [{ role: "user", content: [{ type: "text", text: "hi" }], timestamp: 0 }], tools: [] }), { apiKey: "test", maxRetries: 2, maxRetryDelayMs: 1000 }).result();
 console.log(JSON.stringify({ stopReason: result.stopReason, limit: result.errorMessage?.includes("Server requested 277403s retry delay (max: 1s)") ?? false, detail: result.errorMessage?.includes("rate limited") ?? false, attempts }));
} finally { await new Promise(resolve => server.close(resolve)); }
