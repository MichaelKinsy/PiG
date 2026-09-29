// Pi 0.87.1 model-registry.ts:101-103,165-167 returns the same callable Provider in every extension.
import assert from "node:assert/strict";
import { writeFileSync } from "node:fs";

export default function (pi) {
  pi.registerCommand("carrier-probe", { handler: async (path, ctx) => {
    const registry = ctx.modelRegistry;
    const provider = registry.getRegisteredNativeProvider("carrier-provider");
    assert.ok(provider, "foreign native Provider is absent");
    assert.strictEqual(registry.getProvider(provider.id), provider);
    let original;
    pi.events.emit("carrier:identity", value => { original = value; });
    assert.strictEqual(provider, original, "same-cell Provider identity changed");
    const model = provider.getModels()[0];
    assert.ok(!provider.getModels().then, "getModels became async");
    const filtered = provider.filterModels(provider.getModels(), { type: "api_key", key: "selected" });
    assert.ok(!filtered.then, "filterModels became async");
    assert.strictEqual(filtered[0], model);
    assert.deepEqual(provider.filterModels(provider.getModels(), undefined), []);
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
    const output = {
      identity: true, headers: provider.headers, check, auth, apiLogin, login, rotated, oauth,
      order, models: provider.getModels().map(model => model.id),
      result: result.content, simple: simple.content, deferred: deferred.content,
      cancelled: cancelled.stopReason,
    };
    writeFileSync(path, JSON.stringify(output) + "\n");
  } });
}
