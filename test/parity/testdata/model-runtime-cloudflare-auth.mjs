import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./coding", "-run", "^TestModelRuntimeCloudflareCompatUpstream$", "-count=1", "-v"], { encoding: "utf8" });
  const records = output.split("\n").filter(line => line.startsWith("CLOUDFLARE_REQUEST "));
  assert.equal(records.length, [false, true].length, "direct and extension-style requests");
  for (const record of records) console.log(record);
} else {
  assert.equal(process.argv[2], "pi");
  const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
  assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.2");
  const { AuthStorage } = await import(pathToFileURL(join(root, "dist/core/auth-storage.js")));
  const { ModelRuntime } = await import(pathToFileURL(join(root, "dist/core/model-runtime.js")));
  const { ModelRegistry } = await import(pathToFileURL(join(root, "dist/core/model-registry.js")));
  const { complete, resetApiProviders } = await import(pathToFileURL(join(root, "node_modules/@earendil-works/pi-ai/dist/compat.js")));
  for (const extensionStyle of [false, true]) {
    const credentials = AuthStorage.inMemory({ "cloudflare-ai-gateway": { type: "api_key", key: "test-token", env: { CLOUDFLARE_ACCOUNT_ID: "test-account", CLOUDFLARE_GATEWAY_ID: "test-gateway" } } });
    const runtime = await ModelRuntime.create({ credentials, modelsPath: null, allowModelNetwork: false });
    const registry = new ModelRegistry(runtime);
    const model = runtime.getModel("cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6");
    assert.ok(model);
    let calls = 0;
    const fetch = async (input, init) => {
      calls++;
      const request = new Request(input, init);
      assert.equal(request.url, "https://gateway.ai.cloudflare.com/v1/test-account/test-gateway/compat/chat/completions");
      const headers = request.headers;
      assert.equal(headers.get("cf-aig-authorization"), "Bearer test-token");
      assert.equal(headers.has("Authorization"), false);
      assert.equal(headers.has("x-api-key"), false);
      console.log("CLOUDFLARE_REQUEST " + JSON.stringify([extensionStyle, request.url, headers.get("cf-aig-authorization"), headers.has("Authorization"), headers.get("Authorization") ?? "", headers.has("x-api-key"), headers.get("x-api-key") ?? ""]));
      return new Response('data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}\n\ndata: [DONE]\n\n', { status: 200, headers: { "content-type": "text/event-stream" } });
    };
    resetApiProviders();
    let result;
    if (extensionStyle) {
      const auth = await registry.getApiKeyAndHeaders(model);
      assert.equal(auth.ok, true);
      assert.equal(auth.headers.Authorization, null);
      assert.equal(auth.headers["x-api-key"], null);
      result = await complete(model, { messages: [] }, { ...auth, fetch });
    } else {
      result = await runtime.completeSimple(model, { messages: [] }, { fetch });
    }
    assert.equal(result.stopReason, "stop");
    assert.equal(calls, 1, "one model request");
  }
}
