// The Node fixture of the typed model operation and theme conformance rows (model_types_test.go, theme_test.go). It carries the tools of testfixture/modeltypes (Go), testdata/modeltypes-rust and the inline Python fixture, so one table of rows proves every SDK. Every value a row asserts is one the host or this code produces and no SDK fallback can.
import { getMarkdownTheme, getSelectListTheme, getSettingsListTheme, highlightCode, keyHint, keyText, rawKeyHint } from "@earendil-works/pi-coding-agent";
import {
  Key, KeybindingsManager, TUI_KEYBINDINGS, detectCapabilities, getCapabilities, getKeybindings, getTerminalColorMode, isKittyProtocolActive,
  resetCapabilitiesCache, setCapabilities, setCapabilityOverrides, setKeybindings, setKittyProtocolActive,
} from "@earendil-works/pi-tui";

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
      foundWindow: found.contextWindow, ofTypeWindow: registry.getModelOfType("classifier", "typesafe", "jev-latest").contextWindow, ofTypeMissing: registry.getModelOfType("classifier", "typesafe", "missing") ?? null, missing: registry.findOfType("classifier", "typesafe", "missing") ?? null,
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
  tool("virtual_visible", async (_id, _params, _signal, _update, ctx) => {
    const registry = ctx.modelRegistry;
    registry.registerVirtualModel({ provider: "router", id: "seen", name: "Seen", contextWindow: 1000, route });
    const listed = () => registry.getAll().some((model) => model.provider === "router" && model.id === "seen");
    const registered = registry.find("router", "seen") !== undefined;
    const isListed = listed();
    registry.unregisterVirtualModel("router", "seen");
    return reply({ registered, listed: isListed, gone: registry.find("router", "seen") === undefined, unlisted: !listed() });
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
  // tui_probe: what pi-tui's keybindings and capabilities answer once the host published its state (the Go, Rust and Python fixtures carry the same tool).
  tool("tui_probe", async (_id, params) => {
    const list = (keys) => (keys === undefined ? [] : Array.isArray(keys) ? [...keys] : [keys]);
    const caps = (value) => ({ images: value.images ?? null, trueColor: value.trueColor, hyperlinks: value.hyperlinks });
    const keybindings = getKeybindings();
    const ids = params.ids ?? [];
    const original = getCapabilities();
    const report = {
      keys: Object.fromEntries(ids.map((id) => [id, keybindings.getKeys(id)])),
      matches: (params.inputs ?? []).map((input) => keybindings.matches(input.data, input.id)),
      definitions: Object.fromEntries(ids.map((id) => [id, keybindings.getDefinition(id)?.description ?? null])),
      user: Object.fromEntries(Object.entries(keybindings.getUserBindings()).map(([id, keys]) => [id, list(keys)])),
      conflicts: keybindings.getConflicts().map((conflict) => ({ key: conflict.key, keybindings: conflict.keybindings })),
      resolvedIds: Object.keys(keybindings.getResolvedBindings()).sort(),
      defaults: {
        count: Object.keys(TUI_KEYBINDINGS).length,
        "tui.input.submit": list(TUI_KEYBINDINGS["tui.input.submit"].defaultKeys), "tui.select.confirm": list(TUI_KEYBINDINGS["tui.select.confirm"].defaultKeys),
      },
      key: { ctrl: Key.ctrl("x"), alt: Key.alt(Key.enter), shift: Key.shift(Key.tab), escape: Key.escape, ctrlShift: Key.ctrlShift("p") },
      capabilities: caps(original), colorMode: getTerminalColorMode(),
    };
    setKeybindings(new KeybindingsManager({ "x.probe": { defaultKeys: ["ctrl+q"] } }));
    report.installed = { keys: getKeybindings().getKeys("x.probe"), hostKeys: getKeybindings().getKeys("tui.input.submit") };
    getKeybindings().setUserBindings({ "x.probe": ["ctrl+w"] });
    report.installed.rebound = getKeybindings().getKeys("x.probe");
    setKeybindings(keybindings);
    report.installed.restored = getKeybindings().getKeys("tui.input.submit");

    setCapabilities({ images: "kitty", trueColor: false, hyperlinks: false });
    const swapped = { capabilities: caps(getCapabilities()), colorMode: getTerminalColorMode() };
    setCapabilityOverrides({ hyperlinks: true });
    swapped.overridden = getCapabilities().hyperlinks;
    setCapabilityOverrides({});
    resetCapabilitiesCache();
    swapped.detected = typeof detectCapabilities() === "object";
    setCapabilities(original);
    swapped.restored = caps(getCapabilities());
    const before = isKittyProtocolActive();
    setKittyProtocolActive(true);
    const after = isKittyProtocolActive();
    setKittyProtocolActive(before);
    swapped.kitty = [before, after, isKittyProtocolActive()];
    report.swapped = swapped;
    return reply(report);
  });
  tool("ops_status", async () => reply({ cancelled: state.cancelled }));
  // tui_theme_probe: pi-coding-agent's own theme helpers and key hints, which the Go, Rust and Python SDKs answer the same way.
  tool("tui_theme_probe", async (_id, params) => {
    const samples = params.samples ?? [];
    const apply = (style) => samples.map((sample) => style(sample));
    switch (params.op) {
      case "markdown": {
        const theme = getMarkdownTheme();
        const styles = Object.fromEntries(Object.entries(theme).filter(([key]) => key !== "highlightCode").map(([key, style]) => [key, apply(style)]));
        return reply({ styles, highlight: (params.highlight ?? []).map(([code, lang]) => theme.highlightCode(code, lang ?? undefined)) });
      }
      case "select": {
        const theme = getSelectListTheme();
        return reply({ styles: Object.fromEntries(Object.entries(theme).map(([key, style]) => [key, apply(style)])) });
      }
      case "settings": {
        const theme = getSettingsListTheme();
        const pair = (style) => samples.map((sample) => [style(sample, false), style(sample, true)]);
        return reply({ styles: { label: pair(theme.label), value: pair(theme.value), description: apply(theme.description), cursor: theme.cursor, hint: apply(theme.hint) } });
      }
      case "highlight":
        return reply({ lines: highlightCode(params.code ?? "", params.lang ?? undefined) });
      case "key_hint":
        return reply({ keyHint: keyHint(params.id, params.description), keyText: keyText(params.id) });
      case "raw_key_hint":
        return reply({ rawKeyHint: rawKeyHint(params.key, params.description) });
    }
    throw new Error("unknown op " + params.op);
  });
  hostSurface(pi, tool);
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
    return reply({ appearance: theme.appearance ?? null, colors, styles, fgs, queries: themeQueries(theme, params) });
  });
}

// The rest of ctx.ui.theme: modifiers, getFgAnsi/getBgAnsi/bg for the tokens the test passes, getColorMode, the border colors and sourcePath. A throw is "throw:" and its message.
function themeQueries(theme, params) {
  if (!params.queries) return null;
  const attempt = (fn) => {
    try {
      return fn();
    } catch (error) {
      return `throw:${error.message}`;
    }
  };
  const tokens = params.ansiTokens ?? [];
  const modifierStyles = {
    bold: (text) => theme.bold(text), italic: (text) => theme.italic(text), underline: (text) => theme.underline(text),
    inverse: (text) => theme.inverse(text), strikethrough: (text) => theme.strikethrough(text),
  };
  const byToken = (fn) => Object.fromEntries(tokens.map((token) => [token, attempt(() => fn(token))]));
  return {
    modifiers: Object.fromEntries(Object.entries(modifierStyles).map(([name, style]) => [name, style("x")])),
    modifierTexts: Object.fromEntries(Object.entries(modifierStyles).map(([name, style]) => [name, (params.modifierTexts ?? []).map((text) => style(text))])),
    fgAnsi: byToken((token) => theme.getFgAnsi(token)),
    bgAnsi: byToken((token) => theme.getBgAnsi(token)),
    bgs: byToken((token) => theme.bg(token, "x")),
    mode: theme.getColorMode(),
    thinking: Object.fromEntries((params.levels ?? []).map((level) => [level, attempt(() => theme.getThinkingBorderColor(level)("x"))])),
    bash: attempt(() => theme.getBashModeBorderColor()("x")),
    sourcePath: theme.sourcePath ?? null,
    sourceInfo: theme.sourceInfo ?? null,
  };
}

// theme.fg(token, "x"), or "throw:" and the message when the SDK throws, so one unknown token does not hide the others.
function themeFg(theme, token) {
  try {
    return theme.fg(token, "x");
  } catch (error) {
    return `throw:${error.message}`;
  }
}

// The tools of the host-surface rows (host_surface_test.go): onThemeChange, onToolsExpandedChange, a widget component, bell, toolCallIndex, toolInput, extensionSourceInfo, onLoaded and a factory-time events.emit. testfixture/modeltypes (Go), testdata/modeltypes-rust and the inline Python fixture carry the same tools.
function hostSurface(pi, tool) {
  const log = { theme: [], expanded: [], heard: [], loaded: null, themeOff: undefined, expandedOff: undefined, heardAtEmit: null, errorEmitFailed: null, crossOwnAt: null, crossReturnedAt: null, ackHeard: [], ackAtCross: null };
  // A factory's pi.events.emit reaches the listeners subscribed before it, its own included.
  pi.events.on("c3:factory", (data) => { log.heard.push(data); });
  pi.events.emit("c3:factory", { n: 1, from: "factory" });
  // Pi's emit is synchronous: the listener has run when emit returns, and an emit on "error" that nobody hears throws in the factory.
  log.heardAtEmit = log.heard.length;
  try { pi.events.emit("error", { from: "factory" }); log.errorEmitFailed = false; } catch { log.errorEmitFailed = true; }
  // An extension loaded before this one that listens on c3:cross runs first and finishes before emit returns (milliseconds, as the other fixtures report them).
  // An extension loaded before this one answers the c3:cross emit, from inside its listener, with a c3:ack emit: the listener subscribed here, before the emit, hears it.
  pi.events.on("c3:ack", (data) => { log.ackHeard.push(data); });
  pi.events.on("c3:cross", () => { log.crossOwnAt = Date.now(); });
  pi.events.emit("c3:cross", { from: "factory" });
  log.crossReturnedAt = Date.now();
  log.ackAtCross = log.ackHeard.length;
  // The handler takes its time; a request that ran before it finished would read null.
  pi.onLoaded(async (ctx) => {
    await new Promise((resolve) => setTimeout(resolve, 300));
    log.loaded = { cwd: ctx.cwd, mode: ctx.mode, hostCall: typeof ctx.ui.getToolsExpanded() === "boolean", expanded: ctx.ui.getToolsExpanded() };
  });
  const reply = (value) => ({ content: [{ type: "text", text: JSON.stringify(value) }] });
  tool("c3_theme_arm", async (_id, _params, _signal, _update, ctx) => {
    log.themeOff = ctx.ui.onThemeChange((theme) => { log.theme.push(theme.name + "|" + theme.fg("accent", "x")); });
    return reply({ ok: true });
  });
  tool("c3_theme_disarm", async () => { log.themeOff(); return reply({ ok: true }); });
  tool("c3_theme_log", async () => reply({ log: log.theme }));
  tool("c3_expanded_arm", async (_id, _params, _signal, _update, ctx) => {
    log.expandedOff = ctx.ui.onToolsExpandedChange((expanded) => { log.expanded.push(expanded); });
    return reply({ ok: true });
  });
  tool("c3_expanded_disarm", async () => { log.expandedOff(); return reply({ ok: true }); });
  tool("c3_expanded_log", async () => reply({ log: log.expanded }));
  tool("c3_widget_arm", async (_id, params, _signal, _update, ctx) => {
    ctx.ui.setWidget("c3w", () => ({ render: (width) => [`w=${width}|${ctx.ui.theme.name}`], invalidate() {} }), { placement: params.placement });
    return reply({ ok: true });
  });
  tool("c3_widget_clear", async (_id, _params, _signal, _update, ctx) => { ctx.ui.setWidget("c3w", undefined); return reply({ ok: true }); });
  tool("c3_bell", async (_id, _params, _signal, _update, ctx) => { ctx.ui.bell(); return reply({ error: "<nil>" }); });
  tool("c3_tool_call", async (_id, params, _signal, _update, ctx) => reply({
    index: ctx.toolCallIndex ?? null, input: ctx.toolInput ?? null, keys: Object.keys(params), meta: params.meta === undefined ? null : JSON.stringify(params.meta),
  }));
  tool("c3_source", async (_id, _params, _signal, _update, ctx) => reply({ info: ctx.extensionSourceInfo() ?? null }));
  tool("c3_loaded", async () => reply({ loaded: log.loaded, factoryHeard: log.heard, heardAtEmit: log.heardAtEmit, errorEmitFailed: log.errorEmitFailed, crossOwnAt: log.crossOwnAt, crossReturnedAt: log.crossReturnedAt, ackHeard: log.ackHeard, ackAtCross: log.ackAtCross }));
}
