import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.2") throw new Error("Expected Pi 1.0.2");
const { complete } = await import(new URL("dist/compat.js", root));
const cases = JSON.parse(readFileSync(new URL("./completions-metadata.json", import.meta.url), "utf8"));
for (const test of cases) {
  const server = createServer((_req, res) => {
    res.writeHead(200, { "content-type": "text/event-stream" });
    for (const chunk of test.chunks) res.write(`data: ${JSON.stringify(chunk)}\n\n`);
    res.end();
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  try {
    const model = { id: "openrouter/auto", name: "Auto", api: "openai-completions", provider: "openrouter", baseUrl: `http://127.0.0.1:${server.address().port}`, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 8192 };
    const result = await complete(model, { messages: [{ role: "user", content: "hi", timestamp: 0 }] }, { apiKey: "test" });
    console.log(JSON.stringify({ case: test.name, model: result.model, provider: result.provider, responseModel: result.responseModel ?? "", responseId: result.responseId ?? "", rawStopReason: result.rawStopReason ?? "", stopReason: result.stopReason, errorMessage: result.errorMessage ?? "" }));
  } finally {
    await new Promise(resolve => server.close(resolve));
  }
}
