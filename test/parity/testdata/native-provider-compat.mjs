import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { execFileSync } from "node:child_process";

if (process.argv[2] === "pig") {
  process.stdout.write(execFileSync("go", ["run", "./test/parity/testdata/native-provider-compat-go"], { encoding: "utf8" }));
} else {
  const root = process.env.PI_PACKAGE_ROOT;
  const load = path => import(pathToFileURL(join(root, path)));
  const { ModelRuntime } = await load("dist/core/model-runtime.js");
  const { ModelRegistry } = await load("dist/core/model-registry.js");
  const { AuthStorage } = await load("dist/core/auth-storage.js");
  const { InMemoryModelsStore } = await load("node_modules/@earendil-works/pi-ai/dist/models-store.js");
  const { AssistantMessageEventStream } = await load("node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js");
  const model = (id, provider = "extension-oauth", baseUrl = "https://example.test/v1") => ({ id, name: id, provider, baseUrl, api: "openai-completions", reasoning: false, input: ["text"], contextWindow: 1000, maxTokens: 100, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } });
  const unused = () => { throw Error("unused"); };
  const create = (options = {}) => ModelRuntime.create({ credentials: AuthStorage.inMemory(), modelsStore: new InMemoryModelsStore(), modelsPath: null, allowModelNetwork: false, ...options });
  const result = {};
  const runtime = await create();
  const nativeModel = model("native", "extension-native", "https://fallback.test/v1");
  const provider = { id: "extension-native", name: "Extension Native", getModels: () => [nativeModel], stream: unused, streamSimple: unused, auth: { apiKey: { name: "Native setup", login: async interaction => ({ type: "api_key", key: await interaction.prompt({ type: "secret", message: "API key" }) }), check: async ({ credential }) => credential?.key ? { type: "api_key", source: "stored native key" } : undefined, resolve: async ({ credential }) => credential?.key ? { auth: { apiKey: credential.key, baseUrl: "https://resolved.test/v1" }, source: "stored native key" } : undefined } } };
  runtime.registerNativeProvider(provider);
  const registry = new ModelRegistry(runtime);
  assert.equal(registry.getProvider(provider.id), provider);
  assert.equal(registry.getRegisteredNativeProvider(provider.id), provider);
  assert.ok(registry.getRegisteredProviderIds().includes(provider.id));
  assert.ok(registry.find(provider.id, "native"));
  await runtime.login(provider.id, "api_key", { prompt: async prompt => { assert.deepEqual(prompt, { type: "secret", message: "API key" }); return "secret"; }, notify() {} });
  result.auth = await registry.getProviderAuth(provider.id);
  registry.unregisterProvider(provider.id);
  assert.equal(registry.getProvider(provider.id), undefined);
  const dir = await mkdtemp(join(tmpdir(), "native-compat-"));
  try {
    const modelsPath = join(dir, "models.json");
    await writeFile(modelsPath, JSON.stringify({ providers: { "extension-native-deferred": { baseUrl: "https://overlay.test/v1" }, "extension-native": { modelOverrides: { native: { contextWindow: 4242 } } } } }));
    const overlaid = await create({ modelsPath });
    const deferredModel = model("native-deferred", "extension-native-deferred", "https://native.test/v1");
    overlaid.registerNativeProvider({ id: deferredModel.provider, name: "Extension Native Deferred", auth: { apiKey: { name: "Native key", resolve: async () => ({ auth: { apiKey: "key" }, source: "native" }) } }, getModels: () => [deferredModel], stream: unused, streamSimple: unused, fetchDeferred: (requestModel, handle, options) => {
      result.fetch = { baseUrl: requestModel.baseUrl, id: handle.id, apiKey: options.apiKey, wait: options.wait, headers: options.headers, transformForwarded: !!options.transformHeaders };
      const message = { role: "assistant", api: requestModel.api, provider: requestModel.provider, model: requestModel.id, content: [], stopReason: "stop", timestamp: 0, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
      const stream = new AssistantMessageEventStream(); stream.push({ type: "start", partial: message }); stream.push({ type: "done", reason: "stop", message }); stream.end(message); return stream;
    }, cancelDeferred: async (_model, handle, options) => { result.cancel = { id: handle.id, apiKey: options.apiKey, timeoutMs: options.timeoutMs, headers: options.headers, transformForwarded: !!options.transformHeaders }; } });
    const selected = overlaid.getModel(deferredModel.provider, deferredModel.id);
    const handle = { provider: deferredModel.provider, modelId: deferredModel.id, api: deferredModel.api, id: "fetch-id" };
    await overlaid.fetchDeferred(selected, handle, { wait: 25, headers: { "X-Fetch": "fetch" }, transformHeaders: headers => ({ ...headers, "X-Transformed": "fetch" }) });
    await overlaid.cancelDeferred(selected, { ...handle, id: "cancel-id" }, { timeoutMs: 100, transformHeaders: headers => ({ ...headers, "X-Transformed": "cancel" }) });
    assert.equal(result.fetch.baseUrl, "https://overlay.test/v1");
    overlaid.registerNativeProvider({ ...provider, getModels: () => [nativeModel] });
    result.contextWindow = overlaid.getModel(provider.id, "native").contextWindow;
    assert.equal(result.contextWindow, 4242);
  } finally { await rm(dir, { recursive: true, force: true }); }
  const modelsStore = new InMemoryModelsStore();
  const dynamic = await create({ modelsStore });
  dynamic.registerProvider("extension-dynamic", { baseUrl: "http://localhost:8080/v1", api: "openai-completions", apiKey: "local", refreshModels: async () => [model("live", "extension-dynamic", "http://localhost:8080/v1")] });
  await dynamic.refresh({ allowNetwork: false });
  assert.ok(dynamic.getModel("extension-dynamic", "live"));
  result.persisted = (await modelsStore.read("extension-dynamic")) ?? null;
  assert.equal(result.persisted, null);
  const legacy = await create({ credentials: AuthStorage.inMemory({ "extension-oauth": { type: "oauth", access: "access", refresh: "refresh", expires: Date.now() + 60000 } }) });
  legacy.registerProvider("extension-oauth", { baseUrl: "https://example.test/v1", api: "openai-completions", models: [model("base")], oauth: { name: "Extension OAuth", login: unused, refreshToken: async credential => credential, getApiKey: credential => credential.access, modifyModels: (models, credential) => credential.access === "access" ? [...models, model("credential-model")] : models } });
  await legacy.refresh({ allowNetwork: false });
  result.beforeLogout = legacy.getModels("extension-oauth").map(model => model.id);
  await legacy.logout("extension-oauth");
  result.afterLogout = legacy.getModels("extension-oauth").map(model => model.id);
  assert.deepEqual(result.beforeLogout, ["base", "credential-model"]);
  assert.deepEqual(result.afterLogout, ["base"]);
  const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
  console.log(JSON.stringify(canonical(result)));
}
