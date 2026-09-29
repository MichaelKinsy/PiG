// Pi 0.87.1 model-runtime.ts:438-443,753-797: validation of the incoming registration precedes any state change, the effective root is a fresh shallow merge, and children, functions and descriptors are the author's originals. The same scenario runs against Pi's ModelRegistry and PiG's Node ModelRegistry.
export async function run(reg, register, unregister) {
  const out = {};
  let getterReads = 0;
  const models = [{ id: "m1", name: "M1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }];
  const stream = function () { return this; };
  const oauth = { name: "O", login: async () => ({}), refreshToken: async (c) => c, getApiKey: (c) => c.access };
  const config = { baseUrl: "http://x.invalid/v1", apiKey: "k", api: "openai-completions", models, streamSimple: stream, oauth };
  Object.defineProperty(config, "name", { enumerable: true, configurable: true, get() { getterReads++; return "Getter Name"; } });
  register("p", config);
  const a = reg.getRegisteredProviderConfig("p");
  const b = reg.getRegisteredProviderConfig("p");
  out.sameRoot = a === b;
  out.freshRoot = a !== config;
  out.modelsAlias = a.models === models;
  out.streamAlias = a.streamSimple === stream;
  out.oauthAlias = a.oauth === oauth;
  out.nameValue = a.name;
  out.nameIsData = Object.getOwnPropertyDescriptor(a, "name")?.get === undefined;
  out.getterReadsAfterRegister = getterReads;
  a.headers = { reader: "1" };
  out.authorSeesRootWrite = config.headers !== undefined;
  models.push({ ...models[0], id: "m2", name: "M2" });
  out.readerSeesAuthorChild = a.models.length;
  try {
    register("p", { streamSimple: stream });
    out.partialStreamSimple = "accepted";
  } catch (error) {
    out.partialStreamSimple = String(error.message);
  }
  out.rootAfterRejected = reg.getRegisteredProviderConfig("p") === a;
  register("p", { name: "Second" });
  const c = reg.getRegisteredProviderConfig("p");
  out.newRootAfterReregister = c !== a;
  out.mergedReaderWrite = c.headers?.reader ?? null;
  out.mergedName = c.name;
  out.oldRootUnchanged = a.name;
  out.keys = Object.keys(c);
  unregister("p");
  out.afterUnregister = reg.getRegisteredProviderConfig("p") === undefined;
  return out;
}

// Two extensions in one process: B reads A's root, re-registers part of it, and A then reads B's merged root.
export async function runShared(readerA, readerB, registerA, registerB, unregisterB) {
  const out = {};
  const stream = function () { return this; };
  const models = [{ id: "m1", name: "M1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }];
  const config = { name: "A", baseUrl: "http://x.invalid/v1", apiKey: "k", api: "openai-completions", models, streamSimple: stream };
  registerA("shared", config);
  const fromA = readerA.getRegisteredProviderConfig("shared");
  const fromB = readerB.getRegisteredProviderConfig("shared");
  out.sameRootAcrossExtensions = fromA === fromB;
  out.childrenAcrossExtensions = fromB?.models === models && fromB?.streamSimple === stream;
  registerB("shared", { name: "B" });
  const mergedA = readerA.getRegisteredProviderConfig("shared");
  const mergedB = readerB.getRegisteredProviderConfig("shared");
  out.mergedShared = mergedA === mergedB && mergedA !== fromA;
  out.mergedKeepsOtherOwnerFields = mergedA?.streamSimple === stream && mergedA?.models === models && mergedA?.apiKey === "k";
  out.mergedName = mergedA?.name;
  out.ids = readerA.getRegisteredProviderIds().filter((id) => id === "shared");
  unregisterB("shared");
  out.unregisteredForBoth = readerA.getRegisteredProviderConfig("shared") === undefined && readerB.getRegisteredProviderConfig("shared") === undefined;
  return out;
}
