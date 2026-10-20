import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join, resolve, relative } from "node:path";
import { pathToFileURL } from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
const pin = readFileSync("internal/coding/pigversion/pigversion.go", "utf8").match(/UpstreamPackageVersion = "([^"]+)"/)[1];
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, pin);
export const resolvePiPackage = specifier => import.meta.resolve(specifier, pathToFileURL(join(root, "package.json")).href);
const upstream = resolve(".upstream/current/packages/coding-agent");
const helpers = new Set(["test/suite/harness.ts", "test/utilities.ts", "test/model-runtime-test-utils.ts"]);
const cache = new Map();

// Only test helpers are transpiled. Production imports resolve to the unchanged pinned Pi package.
function helper(path) {
  if (cache.has(path)) return cache.get(path);
  assert(helpers.has(path), `unexpected test helper ${path}`);
  let code = ts.transpileModule(readFileSync(join(upstream, path), "utf8"), {
    compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext },
  }).outputText;
  code = code.replace(/from "([^"]+)"/g, (_, specifier) => {
    let target = specifier;
    if (specifier.startsWith(".")) {
      const dependency = relative(upstream, resolve(upstream, dirname(path), specifier));
      target = dependency.startsWith("src/")
        ? pathToFileURL(join(root, "dist", dependency.slice(4).replace(/\.ts$/, ".js"))).href
        : helper(dependency);
    } else if (!specifier.startsWith("node:")) {
      target = resolvePiPackage(specifier);
    }
    return `from ${JSON.stringify(target)}`;
  });
  const url = "data:text/javascript;base64," + Buffer.from(code).toString("base64");
  cache.set(path, url);
  return url;
}

export const { createHarness, getMessageText, getUserTexts } = await import(helper("test/suite/harness.ts"));
export const { fauxAssistantMessage, fauxToolCall, createAssistantMessageEventStream } = await import(resolvePiPackage("@earendil-works/pi-ai"));
export const { Type } = await import(resolvePiPackage("typebox"));

// createSdkHarness builds the session the way Pi's product does: createAgentSession from the pinned package, whose Agent streams through sdk.ts's own
// streamFn (buildRequestOptions, then modelRuntime.streamSimple) and carries the extension hooks. upstream's createHarness builds `new Agent({ streamFn:
// streamSimple })` itself, and agent-session.ts:591 treats that stream function as the stock one. Use this harness where the asserted output depends on the stream
// function (how often a summary request resolves its auth, which hooks it runs); createHarness stays for everything that does not.
// The harness has the same shape as upstream's (session, sessionManager, settingsManager, authStorage, faux, models, getModel, setResponses, cleanup). Tools are off.
export async function createSdkHarness(options = {}) {
  const compat = await import(resolvePiPackage("@earendil-works/pi-ai/compat"));
  const sdk = await import(resolvePiPackage("@earendil-works/pi-coding-agent"));
  const { AuthStorage } = await import(pathToFileURL(join(root, "dist", "core", "auth-storage.js")).href);
  const { createInMemoryModelRegistry, getModelRuntime } = await import(helper("test/model-runtime-test-utils.ts"));
  const { createTestResourceLoader } = await import(helper("test/utilities.ts"));
  const { mkdirSync, rmSync, existsSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const tempDir = join(tmpdir(), `pi-suite-${Date.now()}-${Math.random().toString(36).slice(2)}`);
  mkdirSync(tempDir, { recursive: true });
  const faux = compat.registerFauxProvider({});
  faux.setResponses([]);
  const model = faux.getModel();
  const withConfiguredAuth = options.withConfiguredAuth ?? true;
  const sessionManager = sdk.SessionManager.inMemory();
  const settingsManager = sdk.SettingsManager.inMemory(options.settings);
  const authStorage = AuthStorage.inMemory();
  if (withConfiguredAuth) await authStorage.modify(model.provider, async () => ({ type: "api_key", key: "faux-key" }));
  const modelRegistry = await createInMemoryModelRegistry(authStorage);
  if (withConfiguredAuth) {
    modelRegistry.registerProvider(model.provider, {
      baseUrl: model.baseUrl, apiKey: "faux-key", api: faux.api,
      models: faux.models.map(m => ({ id: m.id, name: m.name, api: m.api, reasoning: m.reasoning, input: m.input, inputLimits: m.inputLimits, cost: m.cost, contextWindow: m.contextWindow, maxTokens: m.maxTokens, baseUrl: m.baseUrl })),
    });
  }
  const { session } = await sdk.createAgentSession({
    cwd: tempDir, modelRuntime: getModelRuntime(modelRegistry), model, settingsManager, sessionManager,
    resourceLoader: createTestResourceLoader(), noTools: "all",
  });
  return {
    session, sessionManager, settingsManager, authStorage, faux, models: faux.models, getModel: faux.getModel, setResponses: faux.setResponses,
    // fauxStreamSimple answers a request the model runtime routed to a provider the caller registered: with the next scripted faux response.
    fauxStreamSimple: compat.streamSimple,
    tempDir,
    cleanup() { session.dispose(); faux.unregister(); if (existsSync(tempDir)) rmSync(tempDir, { recursive: true }); },
  };
}
