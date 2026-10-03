// A provider registered by a command, after the factory finished, carries the same images, classifiers and streamSimple a factory registration does (types.ts:1875-1903, loader.ts:449-457, runner.ts:517-523: once bound, pi.registerProvider takes effect immediately with the whole config).
const ZERO = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
const usage = { ...ZERO, totalTokens: 0, cost: { ...ZERO, total: 0 } };

const config = (suffix) => ({
  api: "late-chat-api", baseUrl: "https://late.test/v1", apiKey: "late-key-" + suffix,
  models: [
    { id: "chat", name: "Chat", reasoning: false, input: ["text"], cost: ZERO, contextWindow: 128000, maxTokens: 4096 },
    { id: "flux", name: "Flux", type: "image", api: "late-images", input: ["text"], output: ["image"], cost: ZERO },
    { id: "cls", name: "Cls", type: "classifier", api: "late-classifier", input: ["text"], contextWindow: 1000, cost: ZERO },
  ],
  images: { "late-images": { generateImages: async (model, request, options) => ({
    api: model.api, provider: model.provider, model: model.id, responseId: options.apiKey,
    output: [{ type: "image", data: "img:" + model.id + ":" + request.input[0].text, mimeType: "image/png" }], stopReason: "stop", timestamp: 1,
  }) } },
  classifiers: { "late-classifier": { classify: async (model, request, options) => ({
    api: model.api, provider: model.provider, model: model.id,
    answers: Object.fromEntries(Object.keys(request.questions).reverse().map((key) => [key, { type: "bool", probability: request.state.text.length / 100 }])),
    stopReason: "stop", timestamp: 2, responseId: options.apiKey,
  }) } },
  streamSimple: async (model, _context, options) => {
    const message = { role: "assistant", api: model.api, provider: model.provider, model: model.id, content: [{ type: "text", text: "late:" + model.id + ":" + options.apiKey }], usage, stopReason: "stop", timestamp: 1 };
    return { async *[Symbol.asyncIterator]() { yield { type: "start", partial: message }; yield { type: "done", reason: "stop", message }; }, result: async () => message };
  },
});

export default function (pi) {
  pi.registerCommand("late-pi", { description: "pi.registerProvider after the factory", handler: async () => { pi.registerProvider("late-pi", config("pi")); } });
  pi.registerCommand("late-registry", { description: "ctx.modelRegistry.registerProvider after the factory", handler: async (_args, ctx) => { ctx.modelRegistry.registerProvider("late-registry", config("registry")); } });
  pi.registerCommand("late-twice", {
    description: "a second late registration that defines no operation",
    handler: async () => { pi.registerProvider("late-twice", config("first")); pi.registerProvider("late-twice", { baseUrl: "https://late-second.test/v1" }); },
  });
  pi.registerCommand("late-gone", {
    description: "a late registration, then its unregistration",
    handler: async () => { pi.registerProvider("late-gone", config("gone")); pi.unregisterProvider("late-gone"); },
  });
}
