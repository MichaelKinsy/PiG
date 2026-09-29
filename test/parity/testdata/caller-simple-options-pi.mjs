import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = join(process.env.PI_PACKAGE_ROOT, "node_modules/@earendil-works/pi-ai");
const pin = readFileSync("internal/coding/pigversion/pigversion.go", "utf8").match(/const UpstreamVersion = "([^"]+)"/)[1];
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, pin);
const { createModels, createProvider } = await import(pathToFileURL(join(root, "dist/models.js")));
const { InMemoryCredentialStore } = await import(pathToFileURL(join(root, "dist/auth/credential-store.js")));
const { InMemoryModelsStore } = await import(pathToFileURL(join(root, "dist/models-store.js")));
const { AssistantMessageEventStream } = await import(pathToFileURL(join(root, "dist/utils/event-stream.js")));
const model = { id: "caller", name: "caller", api: "custom", provider: "caller", baseUrl: "", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128, maxTokens: 16 };
const seen = [];
const respond = (selected, _request, options) => {
  seen.push({ maxTokens: options.maxTokens, reasoning: options.reasoning ?? null, timeoutMs: options.timeoutMs });
  const stream = new AssistantMessageEventStream();
  const message = { role: "assistant", content: [], api: selected.api, provider: selected.provider, model: selected.id, stopReason: "stop", timestamp: 0, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
  stream.push({ type: "done", reason: "stop", message });
  stream.end(message);
  return stream;
};
const models = createModels({ credentials: new InMemoryCredentialStore(), modelsStore: new InMemoryModelsStore() });
models.setProvider(createProvider({ id: "caller", models: [model], auth: { apiKey: { name: "Key", resolve: async () => ({ auth: { apiKey: "test" } }) } }, api: { stream: respond, streamSimple: respond } }));
for (const maxTokens of [0, 98765]) {
  const result = await models.completeSimple(models.getModel("caller", "caller"), { messages: [] }, { maxTokens, timeoutMs: 0 });
  assert.equal(result.stopReason, "stop");
}
assert.deepEqual(seen, [{ maxTokens: 0, reasoning: null, timeoutMs: 0 }, { maxTokens: 98765, reasoning: null, timeoutMs: 0 }]);
console.log(JSON.stringify(seen));
