// Ports packages/coding-agent/src/core/model-registry.ts
import assert from "node:assert/strict";
import { writeFileSync } from "node:fs";

export default function (pi) {
  let retained;
  pi.registerCommand("carrier-capture", { handler: (_path, ctx) => {
    retained = ctx.modelRegistry.getRegisteredNativeProvider("carrier-provider");
    assert.ok(retained);
  } });
  pi.registerCommand("carrier-retained", { handler: async path => {
    const result = await retained.streamSimple(retained.getModels()[0], { messages: [] }).result();
    writeFileSync(path, JSON.stringify(result.content));
  } });
  pi.registerCommand("remote-carrier-probe", { handler: async (path, ctx) => {
    const provider = ctx.modelRegistry.getRegisteredNativeProvider("carrier-provider");
    assert.ok(provider, "foreign native Provider is absent");
    assert.strictEqual(ctx.modelRegistry.getProvider(provider.id), provider);
    assert.strictEqual(ctx.modelRegistry.getRegisteredNativeProvider(provider.id), provider);
    const models = provider.getModels();
    assert.ok(!models.then, "getModels became async");
    const model = models[0];
    const filtered = provider.filterModels(models, { type: "api_key", key: "selected" });
    assert.ok(!filtered.then, "filterModels became async");
    assert.strictEqual(filtered[0], model, "filter lost input model identity");
    assert.deepEqual(provider.filterModels(models, undefined), []);
    const signal = new AbortController().signal;
    const input = { ctx: { env: async name => `injected:${name}`, fileExists: async () => false }, signal };
    const check = await provider.auth.apiKey.check(input);
    const auth = await provider.auth.apiKey.resolve(input);
    const interaction = { signal, prompt: async prompt => prompt.message, notify: () => {} };
    const apiLogin = await provider.auth.apiKey.login(interaction);
    const login = await provider.auth.oauth.login(interaction);
    const rotated = await provider.auth.oauth.refresh(login, signal);
    const oauth = await provider.auth.oauth.toAuth(rotated);
    const order = [];
    await provider.refreshModels({ allowNetwork: false, force: true, signal, publish: async publication => {
      assert.strictEqual(publication.persist, null);
      order.push("persist");
      publication.update();
      order.push("update");
      return true;
    } });
    const stream = provider.stream(model, { messages: [] });
    assert.equal(typeof stream.result, "function", "stream did not return an EventStream immediately");
    const result = await stream.result();
    const simple = await provider.streamSimple(model, { messages: [] }).result();
    const deferred = await provider.fetchDeferred(model, { id: "deferred" }).result();
    await provider.cancelDeferred(model, { id: "deferred" });
    assert.throws(() => provider.stream(model, { messages: [] }, { metadata: { fail: true } }), /carrier stream failed/);
    const controller = new AbortController();
    const waiting = provider.stream(model, { messages: [] }, { signal: controller.signal, metadata: { wait: true } });
    controller.abort();
    const cancelled = await waiting.result();
    writeFileSync(path, JSON.stringify({ headers: provider.headers, check, auth, apiLogin, login, rotated, oauth,
      order, models: provider.getModels().map(model => model.id), result: result.content, simple: simple.content,
      deferred: deferred.content, cancelled: cancelled.stopReason }) + "\n");
  } });
}
