import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
if (process.argv[2] === "pig") {
  console.log(JSON.stringify(canonical(JSON.parse(execFileSync("go", ["run", "./test/parity/testdata/session-model-go"], { encoding: "utf8" })))));
} else {
  const root = process.env.PI_PACKAGE_ROOT;
  const { createAgentSession, ModelRuntime, SessionManager, SettingsManager, DefaultResourceLoader } = await import(root + "/dist/index.js");
  const { AuthStorage } = await import(root + "/dist/core/auth-storage.js");
  const { InMemoryModelsStore } = await import(root + "/node_modules/@earendil-works/pi-ai/dist/models-store.js");
  const { AssistantMessageEventStream } = await import(root + "/node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js");
  const dir = await mkdtemp(join(tmpdir(), "session-model-contract-"));
  const model = (id, reasoning) => ({ id, name: id, api: "openai-completions", provider: "faux", baseUrl: "https://faux.invalid", reasoning, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 8192 });
  try {
    const models = [model("faux-1", true), model("faux-2", false)];
    const runtime = await ModelRuntime.create({ credentials: AuthStorage.inMemory(), modelsStore: new InMemoryModelsStore(), modelsPath: null, allowModelNetwork: false });
    let configured = true, providerUser = "";
    const stream = (model, context) => {
      const user = context.messages.findLast(message => message.role === "user");
      providerUser = typeof user?.content === "string" ? user.content : user?.content.filter(part => part.type === "text").map(part => part.text).join("") ?? "";
      const message = { role: "assistant", content: [{ type: "text", text: "done" }], api: model.api, provider: model.provider, model: model.id, stopReason: "stop", timestamp: 0, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
      const stream = new AssistantMessageEventStream(); stream.push({ type: "start", partial: message }); stream.push({ type: "done", reason: "stop", message }); stream.end(message); return stream;
    };
    runtime.registerNativeProvider({ id: "faux", name: "Faux", auth: { apiKey: { name: "Faux key", resolve: async () => configured ? { auth: { apiKey: "faux-key" }, source: "fixture" } : undefined } }, getModels: () => models, stream, streamSimple: stream });
    await runtime.refresh({ allowNetwork: false });
    const options = [];
    const loader = new DefaultResourceLoader({ cwd: dir, agentDir: dir, noExtensions: true, extensionFactories: [pi => {
      pi.on("input", event => event.text === "ping" ? { action: "handled" } : { action: "transform", text: event.text === "literal-command" ? "/inspect-options" : `transformed:${event.text}` });
      pi.registerCommand("inspect-options", { handler: async (_args, ctx) => { const value = ctx.getSystemPromptOptions(); options.push(value); value.selectedTools.push("mutated_tool"); } });
    }] });
    await loader.reload();
    const { session } = await createAgentSession({ cwd: dir, agentDir: dir, modelRuntime: runtime, model: models[0], resourceLoader: loader, sessionManager: SessionManager.inMemory(dir), settingsManager: SettingsManager.inMemory() });
    session.setScopedModels([{ model: models[0], thinkingLevel: "high" }, { model: models[1] }]);
    session.setThinkingLevel("high");
    const first = await session.cycleModel(); assert.equal(first.model.id, "faux-2"); assert.equal(session.thinkingLevel, "off");
    const second = await session.cycleModel(); assert.equal(second.model.id, "faux-1"); assert.equal(session.thinkingLevel, "high");
    models[0].thinkingLevelMap = { xhigh: "xhigh", max: "max" };
    const levels = [session.cycleThinkingLevel(), session.cycleThinkingLevel(), session.cycleThinkingLevel()]; assert.deepEqual(levels, ["xhigh", "max", "off"]);
    configured = false; await assert.rejects(session.setModel(models[1]), /No API key for faux\/faux-2/); configured = true;
    await session.prompt("hello"); await session.prompt("ping");
    await session.prompt("/inspect-options"); await session.prompt("/inspect-options");
    const priorOptions = [...options[0].selectedTools];
    session.setActiveToolsByName(["read"]);
    await session.prompt("/inspect-options");
    assert.notEqual(options[2], options[0]);
    assert.deepEqual(options[2].selectedTools, ["read", "mutated_tool"]);
    assert.deepEqual(options[0].selectedTools, priorOptions);
    const result = { optionsRebuilt: options[2] !== options[0], rebuiltSelection: options[2].selectedTools, priorOptionsRetained: JSON.stringify(options[0].selectedTools) === JSON.stringify(priorOptions), first: first.model.id, second: second.model.id, levels, rejectedUnauthenticated: true, input: providerUser, users: session.messages.filter(message => message.role === "user").length, optionsShared: options[0] === options[1], optionsRead: options[0].selectedTools.includes("read"), optionsMutation: options[1].selectedTools.includes("mutated_tool") };
    assert.equal(result.input, "transformed:hello"); assert.equal(result.users, 1); assert.ok(result.optionsShared && result.optionsRead && result.optionsMutation);
    await session.prompt("literal-command");
    result.literalCommandCalls = options.length - 3;
    result.literalProviderText = providerUser;
    result.literalUserCount = session.messages.filter(message => message.role === "user").length;
    assert.equal(result.literalCommandCalls, 0);
    assert.equal(result.literalProviderText, "/inspect-options");
    assert.equal(result.literalUserCount, 2);
    console.log(JSON.stringify(canonical(result))); session.dispose();
  } finally { await rm(dir, { recursive: true, force: true }); }
}
