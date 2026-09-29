import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";

const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().filter(key => value[key] !== undefined).map(key => [key, canonical(value[key])])) : value;

if (process.argv[2] === "pig") {
  console.log(JSON.stringify(canonical(JSON.parse(execFileSync("go", ["run", "./test/parity/testdata/registry-metadata-go"], { encoding: "utf8" })))));
} else {
  const root = process.env.PI_PACKAGE_ROOT;
  const { ModelRuntime } = await import(root + "/dist/core/model-runtime.js");
  const { ModelRegistry } = await import(root + "/dist/core/model-registry.js");
  const { AuthStorage } = await import(root + "/dist/core/auth-storage.js");
  const { InMemoryModelsStore } = await import(root + "/node_modules/@earendil-works/pi-ai/dist/models-store.js");
  const { getBuiltinModels } = await import(root + "/node_modules/@earendil-works/pi-ai/dist/providers/all.js");
  const dir = mkdtempSync(join(tmpdir(), "registry-metadata-"));
  try {
    const counter = join(dir, "counter");
    writeFileSync(counter, "0");
    const apiKey = `!sh -c 'count=$(cat "${counter}"); echo $((count + 1)) > "${counter}"; echo key-value'`;
    const provider = apiKey => ({ baseUrl: "https://example.test/v1", api: "openai-completions", apiKey, authHeader: true, models: [{ id: "test-model" }] });
    const modelsPath = join(dir, "models.json");
    writeFileSync(modelsPath, JSON.stringify({ providers: { "custom-provider": provider(apiKey), "failed-provider": provider("!exit 1"), "broken-one": { api: "openai-completions", models: [{ id: "one" }] }, "broken-two": { api: "openai-completions", models: [{ id: "two" }] }, anthropic: { modelOverrides: { "claude-fable-5": { compat: { allowedFallbackModels: [] } } } } } }));
    const credentials = AuthStorage.inMemory();
    const runtime = await ModelRuntime.create({ credentials, modelsStore: new InMemoryModelsStore(), modelsPath, allowModelNetwork: false });
    const registry = new ModelRegistry(runtime);
    runtime.getModels(); registry.getAll(); registry.getAvailable();
    const status = registry.getProviderAuthStatus("custom-provider");
    const count = () => readFileSync(counter, "utf8").trim();
    assert.equal(count(), "0");
    const output = { metadataCommands: count(), status, compositionErrors: { one: registry.getError().includes('Provider "broken-one"'), two: registry.getError().includes('Provider "broken-two"') } };
    output.key = await registry.getApiKeyForProvider("custom-provider");
    const model = registry.find("custom-provider", "test-model");
    output.auth = await registry.getApiKeyAndHeaders(model);
    output.requestCommands = count();
    await credentials.modify("custom-provider", async () => ({ type: "api_key", key: "stored-key" }));
    output.stored = await registry.getApiKeyAndHeaders(model);
    output.storedCommands = count();
    output.failed = await registry.getApiKeyAndHeaders(registry.find("failed-provider", "test-model"));
    output.emptyFallback = registry.find("anthropic", "claude-fable-5").compat.allowedFallbackModels;
    const copilot = getBuiltinModels("github-copilot")[0].id;
    await credentials.modify("github-copilot", async () => ({ type: "oauth", access: "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;", refresh: "github-access-token", expires: Date.now() + 60000, availableModelIds: [copilot] }));
    await runtime.refresh({ allowNetwork: false });
    output.copilot = registry.getAvailable().filter(model => model.provider === "github-copilot").map(model => model.id);
    assert.deepEqual(output.copilot, [copilot]);
    assert.equal(output.requestCommands, "2");
    assert.equal(output.storedCommands, "2");
    console.log(JSON.stringify(canonical(output)));
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
