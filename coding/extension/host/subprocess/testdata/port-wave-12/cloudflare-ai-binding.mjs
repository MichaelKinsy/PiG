import assert from "node:assert/strict";
// Import the same transport helper as upstream's test, relative to this fixture rather than the process launcher.
import { CLOUDFLARE_GATEWAY_BINDING_AUTH_SENTINEL, createAiBindingFetch } from "../../runtime-node/shims/pi-dist/pi-ai/api/cloudflare-ai-binding.js";
// The per-API alias uses the active runtime's provider bridge in both isolated and packed hosts.
import { streamSimpleOpenAICompletions as streamSimple, normalizeContext } from "@earendil-works/pi-ai";

const BINDING_PREFIX = "https://workers-binding.ai/ai-gateway/gateways/my-gateway";
const requestModel = {
  id: "test-model", name: "Test Model", api: "openai-completions", provider: "openai",
  baseUrl: `${BINDING_PREFIX}/openai`, reasoning: false, input: ["text"],
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 10000, maxTokens: 1000,
};
const bindingHeaders = { "cf-aig-authorization": `Bearer ${CLOUDFLARE_GATEWAY_BINDING_AUTH_SENTINEL}`, Authorization: null, "x-api-key": null };

// upstream: packages/ai/test/cloudflare-ai-binding.test.ts:13
function fakeBinding(response) {
  const requests = [];
  const binding = {
    aiGatewayLogId: null,
    fetch: (input, init) => {
      requests.push(input instanceof Request ? input : new Request(input, init));
      return Promise.resolve(response ?? new Response("{}"));
    },
  };
  return { binding, requests };
}

const cases = {
  // upstream: packages/ai/test/cloudflare-ai-binding.test.ts:26
  async "passes requests to the binding untouched"() {
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode("data: {}\n\n"));
        controller.close();
      },
    });
    const bindingResponse = new Response(stream, {
      headers: { "content-type": "text/event-stream", "cf-aig-log-id": "log-1" },
    });
    const { binding, requests } = fakeBinding(bindingResponse);
    const fetchFn = createAiBindingFetch(binding);
    const body = JSON.stringify({ model: "claude", messages: [{ role: "user", content: "hi" }] });
    const response = await fetchFn(`${BINDING_PREFIX}/anthropic/v1/messages?beta=true`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "cf-aig-authorization": `Bearer ${CLOUDFLARE_GATEWAY_BINDING_AUTH_SENTINEL}`,
        "anthropic-version": "2023-06-01",
      },
      body,
    });
    assert.equal(requests[0].url, `${BINDING_PREFIX}/anthropic/v1/messages?beta=true`);
    assert.equal(requests[0].method, "POST");
    assert.deepEqual(Object.fromEntries(requests[0].headers), {
      "content-type": "application/json",
      "cf-aig-authorization": `Bearer ${CLOUDFLARE_GATEWAY_BINDING_AUTH_SENTINEL}`,
      "anthropic-version": "2023-06-01",
    });
    assert.equal(await requests[0].text(), body);
    assert.equal(response, bindingResponse);
    assert.equal(await response.text(), "data: {}\n\n");
  },
  // upstream: packages/ai/test/cloudflare-ai-binding.test.ts:64
  "rejects a binding with no fetch() at construction, not on first request"() {
    assert.throws(() => createAiBindingFetch({ aiGatewayLogId: null }), /does not expose fetch\(\)/);
  },
  // upstream: packages/ai/test/cloudflare-ai-binding.test.ts:70
  async "keeps SDK placeholder auth off the wire when paired with null auth headers"() {
    const { binding, requests } = fakeBinding(Response.json({ error: { type: "bad_request", message: "stubbed" } }, { status: 400 }));
    const model = requestModel;
    const result = await streamSimple(model, normalizeContext({ messages: [{ role: "user", content: "hello", timestamp: 1 }] }), {
      headers: {
        "cf-aig-authorization": `Bearer ${CLOUDFLARE_GATEWAY_BINDING_AUTH_SENTINEL}`,
        Authorization: null, "x-api-key": null,
      },
      fetch: createAiBindingFetch(binding), maxRetries: 0,
    }).result();
    assert.equal(result.stopReason, "error");
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, `${BINDING_PREFIX}/openai/chat/completions`);
    const headerNames = Object.keys(Object.fromEntries(requests[0].headers));
    assert.equal(headerNames.includes("authorization"), false);
    assert.equal(headerNames.includes("x-api-key"), false);
  },
  // Supplemental transport obligations from packages/ai/src/types.ts:147-174.
  async "streams response bytes only after the response observer"() {
    const text = "x".repeat(128 * 1024);
    const bytes = new TextEncoder().encode(`data: ${JSON.stringify({ choices: [{ delta: { content: text }, finish_reason: "stop" }] })}\n\n`);
    let pulls = 0;
    let observed = false;
    const { binding, requests } = fakeBinding(new Response(new ReadableStream({
      pull(controller) { pulls++; controller.enqueue(bytes); controller.close(); },
    }, { highWaterMark: 0 }), { headers: { "content-type": "text/event-stream" } }));
    const result = await streamSimple(requestModel, normalizeContext({ messages: [{ role: "user", content: "hello", timestamp: 1 }] }), {
      headers: bindingHeaders, fetch: createAiBindingFetch(binding), maxRetries: 0,
      onResponse: () => { assert.equal(pulls, 0); observed = true; },
    }).result();
    assert.equal(observed, true);
    assert.equal(result.stopReason, "stop");
    assert.deepEqual(result.content, [{ type: "text", text }]);
    assert.equal(requests.length, 1);
  },
  async "cancels the original fetch when the request signal aborts"() {
    let started;
    let aborted = false;
    const entered = new Promise(resolve => { started = resolve; });
    const controller = new AbortController();
    const binding = {
      aiGatewayLogId: null,
      fetch: (_input, init) => new Promise((_resolve, reject) => {
        const abort = () => { aborted = true; reject(new Error("binding aborted")); };
        init.signal.addEventListener("abort", abort, { once: true });
        if (init.signal.aborted) abort();
        started();
      }),
    };
    const stream = streamSimple(requestModel, normalizeContext({ messages: [{ role: "user", content: "hello", timestamp: 1 }] }), {
      headers: bindingHeaders, fetch: createAiBindingFetch(binding), maxRetries: 0, signal: controller.signal,
    });
    await Promise.race([entered, stream.result().then(() => { throw new Error("stream ended before calling fetch"); })]);
    controller.abort();
    const result = await stream.result();
    assert.equal(result.stopReason, "aborted");
    assert.equal(aborted, true);
  },
};

export default function (pi) {
  pi.registerCommand("binding-cases", { handler: async (name) => {
    assert.equal(typeof cases[name], "function", "unknown binding case");
    await cases[name]();
  } });
}
