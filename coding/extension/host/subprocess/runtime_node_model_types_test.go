package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

func nodeModelTypesScript(t *testing.T, body string) {
	t.Helper()
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("import assert from \"node:assert/strict\";\nimport net from \"node:net\";\nimport { tmpdir } from \"node:os\";\nimport { join } from \"node:path\";\nimport { Runtime, requestSignal } from %q;\n%s", (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String(), body)
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

// The typed reads, getAvailableOfType, classify and the virtual-model calls of ctx.modelRegistry in the Node runtime, without a Host (.upstream/v0.99.2/packages/coding-agent/src/core/model-registry.ts:71-79,135-177,211-217). test/extension-conformance/model_types_test.go proves the same rows against the real Host for every SDK.
//
// The synchronous reads answer from the registry snapshot the host published: chat models from "models", the others from "typedModels", in the host's order (model-registry.ts:145-161). getAvailableOfType and classify ask the host; classify never rejects (model-registry.ts:168-177, model-runtime.ts:800-815).
func TestNodeModelRegistryTypedOperations(t *testing.T) {
	nodeModelTypesScript(t, `
const runtime = new Runtime("/ext/types.mjs");
const calls = [];
let answer;
runtime.conn = { closed: false, callSync(method, args) { calls.push(["sync", method, args]); return null; }, requestState() {}, notify() {} };
runtime.call = async (method, args) => { calls.push(["async", method, args]); if (answer instanceof Error) throw answer; return answer; };
runtime.hostReady = true;
runtime.commitLoad();
const registry = runtime.ctx.modelRegistry;

const chat = [{ id: "gpt", name: "GPT", provider: "openai", api: "openai-completions" }, { id: "opus", name: "Opus", provider: "anthropic", api: "anthropic-messages" }];
const typed = [
  { type: "image", id: "flux", name: "Flux", provider: "openrouter", api: "openrouter-images", output: ["image"] },
  { type: "classifier", id: "jev-latest", name: "Jev", provider: "typesafe", api: "typesafe-system-one", contextWindow: 64000 },
  { type: "classifier", id: "~typesafe/jev-latest", name: "Jev", provider: "openrouter", api: "typesafe-system-one", contextWindow: 64000 },
];
runtime.applyModelRegistryState({ models: chat, typedModels: typed, providers: { typesafe: { name: "TypeSafe", configured: true } }, registered: [] });
const ids = (models) => models.map((model) => model.provider + "/" + model.id);

assert.deepEqual(ids(registry.getModelsOfType("classifier")), ["typesafe/jev-latest", "openrouter/~typesafe/jev-latest"]);
assert.deepEqual(ids(registry.getModelsOfType("chat")), ["openai/gpt", "anthropic/opus"]);
assert.deepEqual(ids(registry.getModelsOfType("image", "openrouter")), ["openrouter/flux"]);
assert.deepEqual(ids(registry.getModelsOfType("image", "typesafe")), []);
assert.equal(registry.findOfType("classifier", "typesafe", "jev-latest").contextWindow, 64000);
assert.equal(registry.getModelOfType("classifier", "typesafe", "jev-latest").contextWindow, 64000);
assert.equal(registry.findOfType("classifier", "typesafe", "missing"), undefined);
assert.equal(registry.getModelOfType("image", "typesafe", "jev-latest"), undefined, "a model of another type is not found");
assert.equal(registry.getModelOfType("chat", "openai", "gpt").id, "gpt");
// A chat model keeps coming from getAll, which is chat only (model-registry.ts:51-53).
assert.deepEqual(ids(registry.getAll()), ["openai/gpt", "anthropic/opus"]);
// A snapshot without typed models has none.
runtime.applyModelRegistryState({ models: chat, providers: {}, registered: [] });
assert.deepEqual(registry.getModelsOfType("classifier"), []);
runtime.applyModelRegistryState({ models: chat, typedModels: typed, providers: {}, registered: [] });

// getAvailableOfType is the host's answer for the type and provider it was asked for (model-registry.ts:135-143).
answer = [typed[1]];
const available = registry.getAvailableOfType("classifier", "typesafe");
assert.ok(available instanceof Promise, "getAvailableOfType returns a Promise");
assert.deepEqual(ids(await available), ["typesafe/jev-latest"]);
assert.deepEqual(calls.at(-1), ["async", "getAvailableOfType", { type: "classifier", provider: "typesafe" }]);
await registry.getAvailableOfType("image");
assert.deepEqual(calls.at(-1), ["async", "getAvailableOfType", { type: "image", provider: undefined }]);
answer = new Error("auth failed");
await assert.rejects(registry.getAvailableOfType("classifier"), /auth failed/);

// classify sends the model, the context and the options without the signal; the host's answer is the result.
const model = typed[1];
const context = { state: { text: "Looks good" }, questions: { tone: { type: "choice", instructions: "Which tone?", criteria: { warm: "Warm" } }, approved: { type: "bool", instructions: "Approved?", criteria: { true: "Yes", false: "No" } } } };
answer = { api: model.api, provider: model.provider, model: model.id, answers: { approved: { type: "bool", probability: 0.1 }, tone: { type: "choice", probabilities: { warm: 1 } } }, stopReason: "stop", timestamp: 2 };
const result = await registry.classify(model, context, { apiKey: "sk-conf", signal: new AbortController().signal });
assert.deepEqual(Object.keys(result.answers), ["approved", "tone"]);
assert.equal(result.answers.approved.probability, 0.1);
assert.deepEqual([calls.at(-1)[1], JSON.parse(JSON.stringify(calls.at(-1)[2]))], ["classify", { model, context, options: { apiKey: "sk-conf" } }]);
await registry.classify(model, context);
assert.deepEqual(JSON.parse(JSON.stringify(calls.at(-1)[2])), { model, context }, "omitted options are not sent");

// A failure is a result that names the model; it never rejects (model-runtime.ts:800-815, model-operations.ts classifierErrorResult).
answer = new Error("classifier unavailable");
const failed = await registry.classify(model, context);
assert.deepEqual([failed.stopReason, failed.errorMessage, failed.provider, failed.model, failed.api, failed.answers], ["error", "classifier unavailable", "typesafe", "jev-latest", "typesafe-system-one", {}]);
assert.equal(typeof failed.timestamp, "number");
// A request whose signal is aborted is reported as aborted, and a pending classification returns when it aborts.
const controller = new AbortController();
answer = new Promise(() => {});
const pending = registry.classify(model, context, { signal: controller.signal });
controller.abort();
const aborted = await pending;
assert.deepEqual([aborted.stopReason, aborted.model], ["aborted", "jev-latest"]);
// A classification whose calling request was cancelled is aborted without a signal too, as the Go, Rust and Python SDKs report it; a failure of a live request stays an error.
const request = { id: "r1", controller: new AbortController(), settled: false, cancelled: false };
answer = new Error("host call cancelled with parent request r1");
const live = await runtime.requestContext.run(request, () => registry.classify(model, context));
assert.equal(live.stopReason, "error");
request.controller.abort();
const cancelled = await runtime.requestContext.run(request, () => registry.classify(model, context));
assert.deepEqual([cancelled.stopReason, cancelled.errorMessage, cancelled.model], ["aborted", "host call cancelled with parent request r1", "jev-latest"]);
`)
}

// registerVirtualModel and unregisterVirtualModel of ctx.modelRegistry are the runtime's: the same declaration and the same host calls as pi.registerVirtualModel (model-registry.ts:211-217, loader.ts:480-495). A refusal is the caller's error and restores the replaced route.
func TestNodeModelRegistryVirtualModels(t *testing.T) {
	nodeModelTypesScript(t, `
const runtime = new Runtime("/ext/virtual.mjs");
const sync = [];
let refuse;
runtime.conn = { closed: false, callSync(method, args) { sync.push([method, args]); if (refuse) { const error = refuse; refuse = undefined; throw error; } return null; }, requestState() {}, notify() {} };
runtime.hostReady = true;
runtime.commitLoad();
const registry = runtime.ctx.modelRegistry;
const route = () => ({ model: { provider: "p", id: "m" }, thinkingLevel: "off" });

registry.registerVirtualModel({ provider: "router", id: "late", name: "Late", contextWindow: 1000, route, ignored: true });
assert.deepEqual([sync.at(-1)[0], JSON.parse(JSON.stringify(sync.at(-1)[1]))], ["registerVirtualModel", { provider: "router", id: "late", name: "Late", contextWindow: 1000 }]);
assert.ok(runtime.virtualModels.has(JSON.stringify(["router", "late"])), "the route is kept for the host's virtual_model_route");

refuse = new Error("virtual model router/claimed is the id of a physical model");
assert.throws(() => registry.registerVirtualModel({ provider: "router", id: "claimed", name: "Claimed", route }), /router\/claimed is the id of a physical model/);
assert.equal(runtime.virtualModels.has(JSON.stringify(["router", "claimed"])), false, "a refused registration leaves no route");

registry.unregisterVirtualModel("router", "late");
assert.deepEqual(sync.at(-1), ["unregisterVirtualModel", { provider: "router", id: "late" }]);
assert.equal(runtime.virtualModels.has(JSON.stringify(["router", "late"])), false);

// The facade's route gets the request alone (model-runtime.ts:963), and pi.registerVirtualModel's also gets a context (loader.ts:497-500).
const seen = [];
const responses = [];
runtime.respond = async (id, result, error) => { responses.push([id, result ?? null, error?.message]); };
const routeRequest = { model: { provider: "router", id: "facade", api: "virtual" }, reason: "request", messages: [] };
const routeCtx = Object.defineProperty(Object.create(runtime.ctx), requestSignal, { value: new AbortController().signal });
const facade = { provider: "router", id: "facade", name: "Facade", route(...args) { seen.push(["facade", args.length, this === facade]); return { model: { provider: "p", id: "m" }, thinkingLevel: "off" }; } };
registry.registerVirtualModel(facade);
assert.deepEqual(JSON.parse(JSON.stringify(sync.at(-1))), ["registerVirtualModel", { provider: "router", id: "facade", name: "Facade" }]);
await runtime.handleRequest("v1", { method: "virtual_model_route", args: { provider: "router", id: "facade", request: routeRequest } }, routeCtx);
runtime.api.registerVirtualModel({ provider: "router", id: "api", name: "Api", route(...args) { seen.push(["api", args.length, args[1] === routeCtx]); return { model: { provider: "p", id: "m" }, thinkingLevel: "off" }; } });
await runtime.handleRequest("v2", { method: "virtual_model_route", args: { provider: "router", id: "api", request: { ...routeRequest, model: { ...routeRequest.model, id: "api" } } } }, routeCtx);
assert.deepEqual(seen, [["facade", 1, true], ["api", 2, true]]);
assert.deepEqual(responses.map(([id, result, error]) => [id, result?.model?.id, error]), [["v1", "m", undefined], ["v2", "m", undefined]]);
`)
}

// A provider config's images and classifiers run in the extension (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1896-1903, core/provider-composer.ts:632-663). The callbacks cannot cross the wire, so the register frame names the APIs (image_apis, classifier_apis) and the host calls back with provider_operation; the callback's result or its error is the answer. A re-registration merges defined values over the previous root (model-runtime.ts:753-766), so one that defines neither keeps the implementations.
func TestNodeProviderConfigImagesAndClassifiers(t *testing.T) {
	nodeModelTypesScript(t, `
const sock = process.platform === "win32"
  ? "\\\\.\\pipe\\pig-provider-ops-" + process.pid
  : join(tmpdir(), "pig-provider-ops-" + process.pid + ".sock");
const frame = new Promise((resolve, reject) => {
  const server = net.createServer((socket) => {
    let buffer = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      if (buffer.length < 4) return;
      const size = buffer.readUInt32BE(0);
      if (buffer.length < 4 + size) return;
      resolve(JSON.parse(buffer.subarray(4, 4 + size).toString("utf8")));
      socket.destroy();
      server.close();
    });
  });
  server.on("error", reject);
  server.listen(sock);
});
process.env.PIG_EXT_SOCKET = sock;

const seen = [];
const ZERO = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
const runtime = new Runtime("/ext/ops.mjs");
runtime.api.registerProvider("ops", {
  baseUrl: "https://ops.test/v1", apiKey: "ops-key",
  models: [
    { id: "flux", name: "Flux", type: "image", api: "test-images", input: ["text"], output: ["image", "text"], cost: ZERO },
    { id: "cls", name: "Cls", type: "classifier", api: "test-classifier", input: ["text"], contextWindow: 1000, cost: ZERO },
  ],
  images: {
    "test-images": { async generateImages(model, context, options) {
      seen.push(["images", model.id, options.signal instanceof AbortSignal]);
      if (context.input[0].text === "fail") throw new Error("image failed");
      return { api: model.api, provider: model.provider, model: model.id, responseId: options.apiKey, output: [{ type: "image", data: "img:" + model.id + ":" + context.input[0].text, mimeType: "image/png" }], stopReason: "stop", timestamp: 1 };
    } },
    "not-an-implementation": {},
  },
  classifiers: {
    "test-classifier": { async classify(model, context, options) {
      seen.push(["classify", model.id, options.signal instanceof AbortSignal]);
      if (context.state.text === "fail") throw new Error("classifier failed");
      if (context.state.text === "hang") { await new Promise((resolve) => options.signal.addEventListener("abort", resolve, { once: true })); seen.push(["cancelled"]); throw new Error("classifier cancelled"); }
      const answers = Object.fromEntries(Object.keys(context.questions).reverse().map((key) => [key, { type: "bool", probability: context.state.text.length / 100 }]));
      return { api: model.api, provider: model.provider, model: model.id, answers, stopReason: "stop", timestamp: 2 };
    } },
  },
});
await runtime.connect();
const { register } = await frame;
runtime.conn.socket.destroy();

assert.equal(register.providers.length, 1);
const [provider] = register.providers;
assert.deepEqual([provider.name, provider.image_apis, provider.classifier_apis, provider.stream_simple], ["ops", ["test-images"], ["test-classifier"], undefined]);
assert.deepEqual(Object.keys(provider.config).sort(), ["apiKey", "baseUrl", "models"], "the callbacks stay in the extension");
assert.equal(provider.config.models.length, 2);

const responses = [];
runtime.respond = async (id, result, error) => { responses.push([id, result ?? null, error?.message]); };
const model = (id, api) => ({ id, name: id, api, provider: "ops", baseUrl: "https://ops.test/v1" });
const image = (text) => ({ kind: "images", api: "test-images", model: model("flux", "test-images"), context: { input: [{ type: "text", text }] }, options: { apiKey: "sk-ops" } });
const question = (text) => ({ kind: "classifiers", api: "test-classifier", model: model("cls", "test-classifier"), context: { state: { text }, questions: { first: { type: "bool", instructions: "One?" }, second: { type: "bool", instructions: "Two?" } } }, options: {} });
// The dispatcher gives every request context its own AbortSignal under requestSignal (runtime.mjs "run the request").
const requestContext = () => Object.defineProperty(Object.create(runtime.ctx), requestSignal, { value: new AbortController().signal });
const run = (id, tool, args, ctx = requestContext()) => runtime.handleRequest(id, { method: "provider_operation", tool, args }, ctx);

await run("i1", "ops", image("a red circle"));
assert.deepEqual(responses.at(-1), ["i1", { api: "test-images", provider: "ops", model: "flux", responseId: "sk-ops", output: [{ type: "image", data: "img:flux:a red circle", mimeType: "image/png" }], stopReason: "stop", timestamp: 1 }, undefined]);
await run("i2", "ops", image("fail"));
assert.deepEqual(responses.at(-1), ["i2", null, "image failed"], "a callback failure is the request's error");
await run("c1", "ops", question("four"));
const classified = responses.at(-1)[1];
assert.deepEqual([Object.keys(classified.answers), classified.answers.second.probability, classified.model], [["second", "first"], 0.04, "cls"]);
await run("c2", "ops", question("fail"));
assert.deepEqual(responses.at(-1), ["c2", null, "classifier failed"]);
assert.deepEqual(seen.filter(([kind]) => kind !== "cancelled").map((entry) => entry.join(":")), ["images:flux:true", "images:flux:true", "classify:cls:true", "classify:cls:true"], "each callback saw the request's AbortSignal");

// The request's cancellation reaches the callback's signal.
const controller = new AbortController();
const cancellable = Object.defineProperty(Object.create(runtime.ctx), requestSignal, { value: controller.signal });
const hanging = run("c3", "ops", question("hang"), cancellable);
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(seen.some(([kind]) => kind === "cancelled"), false);
controller.abort();
await hanging;
assert.deepEqual(responses.at(-1), ["c3", null, "classifier cancelled"]);
assert.equal(seen.some(([kind]) => kind === "cancelled"), true);

// An API without an implementation, a provider without one and an unknown kind are errors.
await run("u1", "ops", { ...image("x"), api: "other-images" });
assert.match(responses.at(-1)[2], /Provider ops has no image implementation for "other-images"/);
await run("u2", "ops", { ...question("x"), api: "other-classifier" });
assert.match(responses.at(-1)[2], /Provider ops has no classifier implementation for "other-classifier"/);
await run("u3", "nope", image("x"));
assert.match(responses.at(-1)[2], /Provider nope has no image implementation/);
await run("u4", "ops", { ...image("x"), kind: "other" });
assert.match(responses.at(-1)[2], /Unknown provider operation "other"/);

// A re-registration that defines neither keeps the implementations; one that defines images replaces only those (model-runtime.ts:753-766). The connection is gone, so the registrations stay queued, as a loading factory's do.
runtime.providersSent = false;
runtime.api.registerProvider("ops", { name: "Ops again" });
await run("k1", "ops", image("kept"));
assert.equal(responses.at(-1)[1].output[0].data, "img:flux:kept");
runtime.api.registerProvider("ops", { images: { "test-images": { generateImages: async (model) => ({ api: model.api, provider: model.provider, model: model.id, output: [], stopReason: "stop", timestamp: 3 }) } } });
await run("k2", "ops", image("replaced"));
assert.equal(responses.at(-1)[1].timestamp, 3);
await run("k3", "ops", question("four"));
assert.equal(responses.at(-1)[1].stopReason, "stop", "the classifier implementation was not replaced");

runtime.api.unregisterProvider("ops");
await run("g1", "ops", image("gone"));
assert.match(responses.at(-1)[2], /Provider ops has no image implementation/);
`)
}

// A provider registered after the factory finished (pi.registerProvider or ctx.modelRegistry.registerProvider from a command or event) carries the same images, classifiers and streamSimple a factory registration does: Pi takes the whole ProviderConfig at any time (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803,1875-1903, loader.ts:449-457, runner.ts:517-523). The callbacks stay in the extension; the host call names them as the register frame does (image_apis, classifier_apis, stream_simple) and the host runs each through provider_operation and provider_stream_simple.
func TestNodeLateProviderRegistrationDeclaresItsOperations(t *testing.T) {
	nodeModelTypesScript(t, `
const ZERO = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
const runtime = new Runtime("/ext/late.mjs");
// The factory finished: the register frame went out and a host call reaches the host at once.
const calls = [];
runtime.providersSent = true;
runtime.fireAndForget = (method, args) => { calls.push([method, args]); };
// The config's cross-process reference needs a live connection, which this rig has none of.
runtime.providerConfigRef = () => "ref";

const config = (suffix) => ({
  api: "late-chat-api", baseUrl: "https://late.test/v1", apiKey: "late-key-" + suffix,
  models: [{ id: "flux", name: "Flux", type: "image", api: "late-images", input: ["text"], output: ["image"], cost: ZERO }],
  images: { "late-images": { generateImages: async (model, request, options) => ({ api: model.api, provider: model.provider, model: model.id, responseId: options.apiKey + ":" + suffix, output: [], stopReason: "stop", timestamp: 1 }) } },
  classifiers: { "late-classifier": { classify: async (model) => ({ api: model.api, provider: model.provider, model: model.id, answers: {}, stopReason: "stop", timestamp: 2 }) } },
  streamSimple: async () => { throw new Error("unused"); },
});
const lastCall = () => calls.at(-1)[1];
const respond = [];
runtime.respond = async (id, result, error) => { respond.push([id, result ?? null, error?.message]); };
const requestContext = () => Object.defineProperty(Object.create(runtime.ctx), requestSignal, { value: new AbortController().signal });
const run = (id, tool, args) => runtime.handleRequest(id, { method: "provider_operation", tool, args }, requestContext());
const image = { kind: "images", api: "late-images", model: { id: "flux", name: "Flux", api: "late-images", provider: "late-pi" }, context: { input: [] }, options: { apiKey: "sk" } };

for (const [name, register] of [["late-pi", (n, c) => runtime.api.registerProvider(n, c)], ["late-registry", (n, c) => runtime.ctx.modelRegistry.registerProvider(n, c)]]) {
  register(name, config(name));
  const [method, args] = calls.at(-1);
  assert.equal(method, "registerProvider");
  assert.equal(args.name, name);
  assert.equal(args.stream_simple, true, name + ": streamSimple is declared");
  assert.deepEqual(args.image_apis, ["late-images"], name);
  assert.deepEqual(args.classifier_apis, ["late-classifier"], name);
  assert.deepEqual(Object.keys(args.config).sort(), ["api", "apiKey", "baseUrl", "models"], name + ": the callbacks stay in the extension");
  await run("i-" + name, name, { ...image, model: { ...image.model, provider: name } });
  assert.equal(respond.at(-1)[1].responseId, "sk:" + name, name + ": the extension runs the implementation");
}

// A later registration that defines no operation declares none and keeps the implementations of the one before (model-runtime.ts:753-766).
runtime.api.registerProvider("late-pi", { baseUrl: "https://late.test/v2" });
assert.deepEqual([lastCall().stream_simple, lastCall().image_apis, lastCall().classifier_apis], [true, ["late-images"], ["late-classifier"]], "the registration declares what the extension keeps for the provider");
await run("kept", "late-pi", { ...image, model: { ...image.model, provider: "late-pi" } });
assert.equal(respond.at(-1)[1].responseId, "sk:late-pi");

runtime.api.unregisterProvider("late-pi");
await run("gone", "late-pi", image);
assert.match(respond.at(-1)[2], /Provider late-pi has no image implementation/);
`)
}
