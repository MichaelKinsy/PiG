import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.0") throw new Error("Expected Pi 1.0.0");
const { stream } = await import(new URL("dist/api/openai-responses.js", root));
const { normalizeContext } = await import(new URL("dist/utils/transcript.js", root));
for (const [label, id, provider] of [["same", "gpt-5.4", "openai"], ["model-switch", "gpt-5.2", "openai"], ["provider-switch", "gpt-5.4", "azure-openai-responses"]]) {
 for (const custom of [false, true]) {
  const name = custom ? "query" : "lookup";
  const itemId = custom ? "ctc_test" : "fc_test";
  const args = custom ? { input: "hello" } : { value: "hello" };
  const tools = custom ? [{ name, description: "", parameters: { type: "object", properties: { input: { type: "string" } }, required: ["input"] }, constrainedSampling: { type: "grammar", variants: { "openai_lark": "start: /[a-z]+/" } } }] : [];
  const cost = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
  const model = { id, name: id, provider, api: "openai-responses", baseUrl: "https://example.invalid", reasoning: true, input: ["text"], cost, contextWindow: 400000, maxTokens: 128000, compat: { supportsOpenAIGrammarTools: true } };
  let payload;
  const result = await stream(model, normalizeContext({ tools, messages: [{ role: "assistant", api: "openai-responses", provider: "openai", model: "gpt-5.4", stopReason: "toolUse", content: [{ type: "toolCall", id: `call_test|${itemId}`, name, arguments: args, namespace: "dynamic_tools" }], usage: { ...cost, totalTokens: 0, cost: { ...cost, total: 0 } }, timestamp: 0 }] }), { apiKey: "test", onPayload(value) { payload = value; throw new Error("captured"); } }).result();
  if (!result.errorMessage?.includes("captured")) throw new Error(result.errorMessage);
  const call = payload.input.find(item => item.type === "function_call" || item.type === "custom_tool_call");
  if (!call) throw new Error("missing replayed tool call");
  console.log(`${label} ${call.name} ${label === "same" ? call.id : ""} ${JSON.stringify(call.namespace ?? "")}`);
 }
}
