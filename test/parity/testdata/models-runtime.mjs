import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

if (process.argv[2] === "pig") {
  process.stdout.write(execFileSync("go", ["run", "./test/parity/testdata/models-runtime-go"], { encoding: "utf8" }));
} else {
  const root = join(process.env.PI_PACKAGE_ROOT, "node_modules/@earendil-works/pi-ai/dist");
  const { createModels, createProvider } = await import(pathToFileURL(join(root, "models.js")));
  const { envApiKeyAuth } = await import(pathToFileURL(join(root, "auth/helpers.js")));
  const { InMemoryCredentialStore } = await import(pathToFileURL(join(root, "auth/credential-store.js")));
  const { InMemoryModelsStore } = await import(pathToFileURL(join(root, "models-store.js")));
  const { AssistantMessageEventStream } = await import(pathToFileURL(join(root, "utils/event-stream.js")));
  const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
  const model = id => ({ id, name: id, api: "test-api", provider: "p1", baseUrl: "https://example.test/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 10000, maxTokens: 1000 });
  const credentials = new InMemoryCredentialStore();
  await credentials.modify("p1", async () => ({ type: "api_key", key: "stored-key" }));
  const modelsStore = new InMemoryModelsStore();
  const models = createModels({ credentials, modelsStore });
  let fetchedCredential, force, call;
  let transforms = 0;
  const respond = (model, transcript, options) => {
    call = { model: model.id, baseUrl: model.baseUrl, apiKey: options.apiKey, headers: options.headers, env: options.env, transformForwarded: "transformHeaders" in options, messages: transcript.messages };
    const stream = new AssistantMessageEventStream();
    const message = { role: "assistant", content: [{ type: "text", text: "ok" }], api: model.api, provider: model.provider, model: model.id, stopReason: "stop", timestamp: 0, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
    stream.push({ type: "start", partial: message }); stream.push({ type: "done", reason: "stop", message }); stream.end(message); return stream;
  };
  models.setProvider(createProvider({ id: "p1", models: [model("baseline")], auth: { apiKey: { name: "Key", resolve: async ({ credential }) => ({ auth: { apiKey: credential?.key ?? "ambient-key", baseUrl: "https://auth.test/v1", headers: { "X-Shared": "auth", "x-provider": "provider" } }, env: { PROVIDER: "provider", SHARED: "provider" }, source: "stored" }) } }, fetchModels: async refresh => { fetchedCredential = refresh.credential; force = refresh.force; return [model("dynamic")]; }, api: { stream: respond, streamSimple: respond } }));
  const refresh = await models.refresh({ force: true });
  if (refresh.aborted || refresh.errors.size) throw new Error("refresh failed");
  const stored = (await modelsStore.read("p1")).models;
  const list = models.getModels().map(model => model.id);
  const selected = models.getModel("p1", "dynamic");
  selected.headers = { "x-model": "model", "X-Shared": "model" };
  const request = { messages: [{ role: "user", content: "hi", timestamp: 7 }] };
  const stream = models.streamSimple(selected, request, { apiKey: "explicit-key", headers: { "x-shared": "request" }, env: { REQUEST: "request", SHARED: "request" }, transformHeaders: async headers => { transforms++; return { ...headers, "x-transformed": "yes" }; } });
  const events = []; for await (const event of stream) events.push(event.type);
  const message = await stream.result();
  let publication;
  models.setProvider({ id: "old", name: "old", getModels: () => [], refreshModels: async refresh => { publication = refresh.publish; } });
  await models.refresh({ providers: ["old"], allowNetwork: false });
  models.setProvider({ id: "old", name: "replacement" });
  let staleUpdated = false;
  const staleAccepted = await publication({ update: () => { staleUpdated = true; } });
  const ghost = await models.completeSimple({ ...model("ghost-model"), provider: "ghost" }, request);
  const emptyModels = createModels({ authContext: { env: async name => name === "EMPTY" ? "" : "fallback-key", fileExists: async () => false } });
  emptyModels.setProvider({ id: "env", name: "Env", auth: { apiKey: envApiKeyAuth("Key", ["EMPTY", "FALLBACK"]) } });
  const envFallback = (await emptyModels.getAuth("env")).auth.apiKey;
  console.log(JSON.stringify(canonical({ envFallback, models: list, stored, credential: fetchedCredential, force, call, events, result: { content: message.content, stopReason: message.stopReason }, transforms, staleAccepted, staleUpdated, unknownProvider: ghost.errorMessage })));
}
