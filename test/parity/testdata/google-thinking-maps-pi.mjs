import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.1") throw new Error("Expected Pi 1.0.1");
const { streamSimple: google } = await import(new URL("dist/api/google-generative-ai.js", root));
const { streamSimple: vertex } = await import(new URL("dist/api/google-vertex.js", root));
const { normalizeContext } = await import(new URL("dist/utils/transcript.js", root));
const cases = JSON.parse(readFileSync(new URL("google-thinking-maps.json", import.meta.url), "utf8"));
for (const [adapter, api, stream] of [["google", "google-generative-ai", google], ["vertex", "google-vertex", vertex]]) {
 for (const test of cases) {
  const model = { id: test.id, name: test.id, provider: `test-${adapter}`, api, baseUrl: "https://example.invalid/v1", reasoning: true, thinkingLevelMap: test.mapping, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 };
  let payload;
  const result = await stream(model, normalizeContext({ messages: [{ role: "user", content: "Hello", timestamp: 0 }] }), { apiKey: "test", reasoning: test.level || undefined, thinkingBudgets: test.budget ? { high: test.budget } : undefined, onPayload(value) { payload = value; throw new Error("payload captured"); } }).result();
  if (!result.errorMessage?.includes("payload captured")) throw new Error(result.errorMessage);
  console.log(`${adapter}/${test.name} ${JSON.stringify(payload.config.thinkingConfig)}`);
 }
}
