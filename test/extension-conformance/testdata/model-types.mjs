// The Node fixture of the typed model operation and theme conformance rows (model_types_test.go, theme_test.go). It carries the tools of testfixture/modeltypes (Go), testdata/modeltypes-rust and the inline Python fixture, so one table of rows proves every SDK. Every value a row asserts is one the host or this code produces and no SDK fallback can.
const ZERO = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
const state = { cancelled: 0 };

async function imageImpl(model, request, options) {
  const prompt = request.input[0].text;
  if (prompt === "fail") throw new Error("image failed");
  return {
    api: model.api, provider: model.provider, model: model.id, responseId: options.apiKey,
    output: [{ type: "image", data: "img:" + model.id + ":" + prompt, mimeType: "image/png" }], stopReason: "stop", timestamp: 1,
  };
}

async function classifyImpl(model, request, options) {
  const text = request.state.text;
  if (text === "fail") throw new Error("classifier failed");
  if (text === "hang") {
    await new Promise((resolve) => options.signal.addEventListener("abort", resolve, { once: true }));
    state.cancelled++;
    throw new Error("classifier cancelled");
  }
  const answers = {};
  for (const key of Object.keys(request.questions).reverse()) answers[key] = { type: "bool", probability: (text.length + 10 * (request.images?.length ?? 0)) / 100 };
  return { api: model.api, provider: model.provider, model: model.id, answers, stopReason: "stop", timestamp: 2 };
}

const approval = (text) => ({
  state: { text },
  questions: {
    tone: { type: "choice", instructions: "Which tone?", criteria: { warm: "Warm", cold: "Cold" } },
    approved: { type: "bool", instructions: "Does this express approval?", criteria: { true: "Approval", false: "No approval" } },
  },
});

const ids = (models) => models.map((model) => model.provider + "/" + model.id);
const route = () => ({ model: { provider: "p", id: "m" }, thinkingLevel: "off" });
const reply = (value) => ({ content: [{ type: "text", text: JSON.stringify(value) }] });

export default function (pi) {
  pi.registerProvider("ops", {
    baseUrl: "https://ops.test/v1", apiKey: "ops-key",
    models: [
      { id: "flux", name: "Flux", type: "image", api: "test-images", input: ["text"], output: ["image", "text"], cost: ZERO },
      { id: "cls", name: "Cls", type: "classifier", api: "test-classifier", input: ["text"], contextWindow: 1000, cost: ZERO },
    ],
    images: { "test-images": { generateImages: imageImpl } },
    classifiers: { "test-classifier": { classify: classifyImpl } },
  });
  pi.registerProvider({
    id: "pixels", name: "Pixels",
    auth: { apiKey: { name: "Pixels key", resolve: async () => ({ auth: { apiKey: "pixels-key" } }) } },
    getModels: () => [
      { id: "flux", name: "Flux", type: "image", api: "test-images", input: ["text"], output: ["image"], cost: ZERO, baseUrl: "https://pixels.test/v1" },
      { id: "cls", name: "Cls", type: "classifier", api: "test-classifier", input: ["text"], contextWindow: 1000, cost: ZERO, baseUrl: "https://pixels.test/v1" },
    ],
    stream: () => { throw new Error("unused"); },
    streamSimple: () => { throw new Error("unused"); },
    generateImages: imageImpl,
    classify: classifyImpl,
  });

  const tool = (name, execute) => pi.registerTool({ name, label: name, description: name, parameters: { type: "object" }, execute });
  tool("typed_reads", async (_id, _params, _signal, _update, ctx) => {
    const registry = ctx.modelRegistry;
    const found = registry.findOfType("classifier", "typesafe", "jev-latest");
    return reply({
      classifiers: ids(registry.getModelsOfType("classifier")), chat: ids(registry.getModelsOfType("chat")),
      images: ids(registry.getModelsOfType("image", "openrouter")), available: ids(await registry.getAvailableOfType("classifier", "typesafe")),
      foundWindow: found.contextWindow, missing: registry.findOfType("classifier", "typesafe", "missing") ?? null,
    });
  });
  tool("classify_probe", async (_id, _params, _signal, _update, ctx) => {
    const registry = ctx.modelRegistry;
    const model = registry.findOfType("classifier", "typesafe", "jev-latest");
    const result = await registry.classify(model, approval("Looks good"), { apiKey: "sk-conf" });
    const failed = await registry.classify(model, approval("fail"));
    const withImages = await registry.classify(model, { ...approval("Looks good"), images: [{ type: "image", data: "aW1hZ2U=", mimeType: "image/png" }] });
    return reply({
      stop: result.stopReason, answers: Object.keys(result.answers), approved: result.answers.approved.probability, imagesApproved: withImages.answers.approved.probability, model: result.model,
      failedStop: failed.stopReason, failedMessage: failed.errorMessage ?? null, failedProvider: failed.provider,
    });
  });
  tool("images_probe", async (_id, _params, _signal, _update, ctx) => {
    const registry = ctx.modelRegistry;
    const model = registry.findOfType("image", "openrouter", "flux");
    const prompt = { input: [{ type: "text", text: "a red circle" }] };
    const result = await registry.generateImages(model, prompt, { apiKey: "sk-img" });
    const failed = await registry.generateImages({ ...model, id: "gone" }, prompt);
    return reply({
      stop: result.stopReason, model: result.model, output: result.output,
      failedStop: failed.stopReason, failedMessage: failed.errorMessage ?? null, failedModel: failed.model, failedOutput: failed.output,
    });
  });
  tool("virtual_probe", async (_id, _params, _signal, _update, ctx) => {
    const registry = ctx.modelRegistry;
    registry.registerVirtualModel({ provider: "router", id: "late", name: "Late", contextWindow: 1000, route });
    let refused = "";
    try {
      registry.registerVirtualModel({ provider: "router", id: "claimed", name: "Claimed", route });
    } catch (error) {
      refused = String(error.message).trim();
    }
    registry.unregisterVirtualModel("router", "late");
    return reply({ first: true, refused });
  });
  // late_provider: a provider registered after the factory finished, with the whole config (Pi: types.ts:1875-1903, loader.ts:449-457, runner.ts:517-523).
  tool("late_provider", async (_id, params, _signal, _update, ctx) => {
    const registry = ctx.modelRegistry;
    registry.registerProvider("late", {
      api: "late-chat-api", baseUrl: "https://late.test/v1", apiKey: "late-key",
      models: [
        { id: "chat", name: "Chat", reasoning: false, input: ["text"], cost: ZERO, contextWindow: 1000, maxTokens: 100 },
        { id: "flux", name: "Flux", type: "image", api: "late-images", input: ["text"], output: ["image"], cost: ZERO },
        { id: "cls", name: "Cls", type: "classifier", api: "late-classifier", input: ["text"], contextWindow: 1000, cost: ZERO },
      ],
      images: { "late-images": { generateImages: imageImpl } },
      classifiers: { "late-classifier": { classify: classifyImpl } },
      streamSimple: async (model, _context, options) => {
        const usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
        const message = { role: "assistant", api: model.api, provider: model.provider, model: model.id, content: [{ type: "text", text: "late:" + model.id + ":" + options.apiKey }], usage, stopReason: "stop", timestamp: 1 };
        return { async *[Symbol.asyncIterator]() { yield { type: "done", reason: "stop", message }; }, result: async () => message };
      },
    });
    if (params.mode === "again") registry.registerProvider("late", { baseUrl: "https://late.test/v2" });
    if (params.mode === "gone") registry.unregisterProvider("late");
    return reply({ mode: params.mode ?? null });
  });
  tool("ops_status", async () => reply({ cancelled: state.cancelled }));
  // theme_probe: the host's palette through ctx.ui.theme, for the colors and styles the test passes in params.
  tool("theme_probe", async (_id, params, _signal, _update, ctx) => {
    const theme = ctx.ui.theme;
    const styles = (params.cases ?? []).map((options) => {
      try {
        return { ok: theme.style("x", options) };
      } catch (error) {
        return { error: error.message };
      }
    });
    const colors = Object.fromEntries((params.tokens ?? []).map((token) => [token, theme.colors[token] ?? null]));
    const fgs = Object.fromEntries((params.fgTokens ?? []).map((token) => [token, themeFg(theme, token)]));
    return reply({ appearance: theme.appearance ?? null, colors, styles, fgs });
  });
}

// theme.fg(token, "x"), or "throw:" and the message when the SDK throws, so one unknown token does not hide the others.
function themeFg(theme, token) {
  try {
    return theme.fg(token, "x");
  } catch (error) {
    return `throw:${error.message}`;
  }
}
