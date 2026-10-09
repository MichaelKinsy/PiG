import zlib from "node:zlib";
import { Box, Container, HStack, Image, Loader, Markdown, SelectList, SettingsList, Spacer, Text, TruncatedText, VStack } from "@earendil-works/pi-tui";
import {
  AssistantMessageComponent, BashExecutionComponent, createGrepToolDefinition, createLsToolDefinition, createReadToolDefinition, DynamicBorder,
  getMarkdownTheme, getSelectListTheme, getSettingsListTheme, renderDiff, ToolExecutionComponent, UserMessageComponent,
} from "@earendil-works/pi-coding-agent";

export default function (pi) {
  let schemaRejected = false;
  try { pi.registerTool({ name: "schema-invalid", description: "Must not register", parameters: null, execute: async () => ({ content: [] }) }); }
  catch (error) { schemaRejected = error.message.endsWith('must define an object parameter schema.'); }
  pi.registerCommand("schema-probe", { description: "Report schema rejection", handler: async (_args, ctx) => ctx.ui.notify(`schema-rejected:${schemaRejected}`, "info") });
  for (const [name, type, value] of [["flag-true", "boolean", true], ["flag-false", "boolean", false], ["flag-string", "string", "default"], ["flag-empty", "string", ""], ["flag-unset", "string", undefined]]) {
    pi.registerFlag(name, { type, default: value });
  }
  pi.registerCommand("flag-probe", {
    description: "Report registered flag values",
    handler: async (_args, ctx) => {
      ctx.ui.notify(JSON.stringify(["flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"].map(name => pi.getFlag(name))), "info");
    },
  });
  pi.registerCommand("timeout-probe", { description: "Send JavaScript-number timeouts", handler: async (_args, ctx) => {
    for (const timeout of [0.5, 4294967296.5, 1e21]) await pi.exec("timeout-command", [], { timeout });
    await ctx.ui.select("timeout", ["a"], { timeout: 1500.5 });
  }});
  pi.registerCommand("exec-reject-probe", { description: "Report exec outcomes", handler: async (args, ctx) => {
    for (const command of JSON.parse(args)) {
      try {
        ctx.ui.notify(`resolved:${(await pi.exec(command, [])).code}`, "info");
      } catch (error) {
        ctx.ui.notify(`rejected:${error.message}`, "info");
      }
    }
  }});
  pi.registerCommand("thinking-model", { description: "Read the thinking level and switch the model", handler: async (_args, ctx) => {
    pi.setThinkingLevel("high");
    const ok = await pi.setModel({ provider: "probe", id: "model" });
    ctx.ui.notify(JSON.stringify([pi.getThinkingLevel(), ok]), "info");
  }});
  pi.registerCommand("active_tools_set", { description: "Set the active tools to the comma-separated names", handler: async (args) => pi.setActiveTools(String(args).trim().split(",")) });
  pi.registerCommand("session_actions_unbound", { description: "Report what unbound session actions answer", handler: async (_args, ctx) => {
    const parts = [];
    for (const [name, call] of [["new", () => ctx.newSession()], ["fork", () => ctx.fork("entry")], ["navigate", () => ctx.navigateTree("entry")], ["switch", () => ctx.switchSession("/s.jsonl")]]) {
      try { parts.push(`${name}=${(await call()).cancelled}`); } catch (error) { parts.push(`${name}=error:${error.message}`); }
    }
    try { await ctx.reload(); parts.push("reload=ok"); } catch (error) { parts.push(`reload=error:${error.message}`); }
    ctx.ui.notify(`unbound:${parts.join(",")}`, "info");
  }});
  pi.registerCommand("active_tools_get", { description: "Report the active tools", handler: async (_args, ctx) => ctx.ui.notify(`active_tools:${pi.getActiveTools().join(",")}`, "info") });
  pi.registerCommand("provider_probe_register", { description: "Register a provider with one model", handler: async () => pi.registerProvider("conformance-probe", JSON.parse(`{"baseUrl":"https://probe.invalid/v1","api":"openai-completions","apiKey":"probe-key","models":[{"id":"probe-model","name":"Probe Model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}`)) });
  pi.registerCommand("provider_probe_unregister", { description: "Unregister the provider", handler: async () => pi.unregisterProvider("conformance-probe") });
  pi.registerCommand("commands-probe", { description: "Read the host's slash commands", handler: async (_args, ctx) => {
    ctx.ui.notify(JSON.stringify(pi.getCommands().filter((c) => c.name === "conformance-listed").map((c) => [c.name, c.source, c.description ?? ""])), "info");
  }});
  pi.registerCommand("session-identity", { description: "Read context identity accessors", handler: async (_args, ctx) => {
    const s = ctx.sessionManager;
    ctx.ui.notify(JSON.stringify([s.getSessionId(), s.getSessionFile() ?? null, s.getLeafId() ?? null, pi.getSessionName() ?? null]), "info");
  }});
  pi.registerCommand("registry-session", { description: "Read registry and session facades", handler: async (_args, ctx) => {
    const s = ctx.sessionManager, r = ctx.modelRegistry;
    const out = {
      cwd:s.getCwd(), dir:s.getSessionDir(), id:s.getSessionId(), name:s.getSessionName()??null, leaf:s.getLeafId(),
      entry:s.getEntry("one"), missing:s.getEntry("missing")??null, label:s.getLabel("one")??null,
      entries:s.getEntries(), branch:s.getBranch("one"), tree:s.getTree(), contextEntries:s.buildContextEntries(), projection:s.buildSessionProjection(),
      models:r.getAll(), available:r.getAvailable(), status:r.getProviderAuthStatus("registry-probe"),
      display:r.getProviderDisplayName("registry-probe"), error:r.getError()??null,
      config:r.getRegisteredProviderConfig("registry-probe")??null, ids:r.getRegisteredProviderIds(),
      auth:await r.getProviderAuth("registry-probe"), apiKey:await r.getApiKeyForProvider("registry-probe"),
      missingKey:(await r.getApiKeyForProvider("missing"))??null,
    };
    const refresh=await r.refresh({allowNetwork:false});
    out.refresh={aborted:refresh.aborted,errors:Object.fromEntries([...refresh.errors].map(([key,error])=>[key,error.message]))};
    ctx.ui.notify(JSON.stringify(out),"info");
  }});
  // The session reads of Pi's Pi-order probe (session_read_order_test.go): the answers are written as JSON.stringify writes them.
  pi.registerCommand("session-order", { description: "Read the session as Pi returns it", handler: async (_args, ctx) => {
    const s = ctx.sessionManager;
    ctx.ui.notify(JSON.stringify({
      getEntries: s.getEntries(), getEntry: s.getEntry("a4"), getLeafEntry: s.getLeafEntry(), getBranch: s.getBranch("a4"),
      getChildren: s.getChildren("a1"), getTree: s.getTree(), buildContextEntries: s.buildContextEntries(),
      buildSessionProjection: s.buildSessionProjection(), buildSessionContext: s.buildSessionContext(),
    }), "info");
  }});
  const loginDefinition = () => ({
    brand: Array.from({ length: 5 }, () => "A".repeat(41)),
    hero: Array.from({ length: 14 }, () => "A".repeat(32)),
    mascot: Array.from({ length: 14 }, () => "A".repeat(16)),
    palette: { A: "#123ABC" },
    name: "Conformance Pig",
    description: "Cross-language login fixture",
    tagline: "One canonical definition across every SDK",
  });
  const spriteDefinition = () => ({
    id: "conformance-pig",
    name: "Conformance Pig",
    tagline: "One canonical sprite across every SDK",
    mascot: Array.from({ length: 14 }, () => "A".repeat(16)),
    palette: { A: "#123ABC" },
  });

  pi.registerMessageRenderer("conformance-message", (message, options) => ({
    render(width) { return message.content === "padding-options" ? [JSON.stringify(options)] : [`renderer:${message.content}:expanded=${Boolean(options.expanded)}:width=${width}`]; },
    invalidate() {},
  }));
  pi.registerEntryRenderer("conformance-entry", (entry, options) => ({
    render(width) { return [`entryrenderer:${entry.data}:expanded=${Boolean(options.expanded)}:width=${width}`]; },
    invalidate() {},
  }));
  pi.registerMarkdownTransformer((markdown, context) =>
    `md:${markdown}:${context.messageType}:streaming=${context.isStreaming}:width=${context.availableWidth}`);
  const resolvedLine = (prefix, toolName) => (args) => ({ render: () => [`${prefix}:${toolName}:${args?.q}`], invalidate: () => {} });
  pi.registerToolRenderer((toolName, next) => {
    switch (toolName) {
      case "conformance_tool_renderer": return { renderCall: resolvedLine("resolved", toolName) };
      case "conformance_no_renderer": return undefined;
      case "conformance_fill": return next() ?? { renderCall: resolvedLine("filled", toolName) };
      case "conformance_wrap": return { ...next(), renderCall: resolvedLine("wrapped", toolName) };
      default: return next();
    }
  });
  pi.registerCommand("late_tool_renderer", { description: "Register a tool renderer resolver after loading", handler: async () => {
    pi.registerToolRenderer((toolName, next) => toolName === "conformance_late" ? { renderCall: resolvedLine("late", toolName) } : next());
  } });

  pi.registerTool({
    name: "render_probe",
    description: "Render its own tool card",
    parameters: { type: "object", properties: {} },
    renderShell: "self",
    async execute() { return { content: [{ type: "text", text: "render ok" }] }; },
    renderCall(args, _theme, context) {
      context.state.calls = (context.state.calls ?? 0) + 1;
      const calls = context.state.calls;
      return { render: (width) => [`toolrender:call:${args.topic}:partial=${context.isPartial}:calls=${calls}:width=${width}`], invalidate() {} };
    },
    renderResult(result, options, _theme, context) {
      const calls = context.state.calls;
      return { render: (width) => [`toolrender:result:${result.content[0].text}:${result.details.k}:expanded=${Boolean(options.expanded)}:calls=${calls}:width=${width}:duration=${context.durationMs ?? "none"}`], invalidate() {} };
    },
  });
  pi.registerTool({
    name: "echo",
    description: "Echo input text",
    parameters: { type: "object", required: ["text"], properties: { text: { type: "string", description: "Text to echo" }, offset: { type: "number" } } },
    async execute(_id, params) { return { content: [{ type: "text", text: `echo: ${params.text ?? ""}` }] }; },
  });
  let abortObserved = false;
  pi.registerTool({
    name: "update_tool",
    description: "Stream two partial results",
    parameters: { type: "object", properties: {} },
    async execute(_id, _params, _signal, onUpdate) {
      onUpdate({ content: [{ type: "text", text: "step 1" }] });
      onUpdate({ content: [{ type: "text", text: "step 2" }] });
      return { content: [{ type: "text", text: "done" }] };
    },
  });
  pi.registerTool({
    name: "ordered_details",
    description: "Return details whose members are not in alphabetical order",
    parameters: { type: "object", properties: {} },
    async execute(_id, _params, _signal, onUpdate) {
      onUpdate({ content: [{ type: "text", text: "partial" }], details: { zeta: 1, alpha: { yy: 2, bb: 3 }, mid: [{ qq: 1, aa: 2 }] } });
      return { content: [{ type: "text", text: "done" }], details: { zeta: 1, alpha: { yy: 2, bb: 3 }, mid: [{ qq: 1, aa: 2 }] } };
    },
  });
  pi.registerTool({
    name: "ordered_result",
    description: "Return a result whose members are not in the declared order",
    parameters: { type: "object", properties: {} },
    async execute(_id, _params, _signal, onUpdate) {
      onUpdate({ details: { k: 1 }, content: [{ type: "text", text: "partial" }] });
      return { details: { k: 1 }, isError: true, content: [{ type: "text", text: "done" }] };
    },
  });
  pi.registerTool({
    name: "abort_tool",
    description: "Wait for the abort signal",
    parameters: { type: "object", properties: {} },
    async execute(_id, _params, signal, onUpdate) {
      await new Promise((resolve) => {
        signal.addEventListener("abort", resolve, { once: true });
        onUpdate({ content: [{ type: "text", text: "waiting" }] });
      });
      abortObserved = true;
      return { content: [{ type: "text", text: "aborted" }] };
    },
  });
  // hang_tool ignores its abort signal for longer than the host's abort grace period (D111).
  pi.registerTool({
    name: "hang_tool",
    description: "Ignore the abort signal",
    parameters: { type: "object", properties: {} },
    async execute(_id, _params, _signal, onUpdate) {
      onUpdate({ content: [{ type: "text", text: "waiting" }] });
      await new Promise((resolve) => setTimeout(resolve, 8000));
      return { content: [{ type: "text", text: "late" }] };
    },
  });
  pi.registerCommand("abort_probe", {
    description: "Report whether abort_tool saw its abort signal",
    handler: async (_args, ctx) => ctx.ui.notify(`abort:${abortObserved}`, "info"),
  });
  pi.registerTool({
    name: "rich_tool",
    description: "Return text, image, and terminate",
    parameters: { type: "object", properties: {} },
    async execute() {
      return {
        content: [
          { type: "text", text: "  padded  " },
          { type: "image", data: "aW1n", mimeType: "image/png" },
          { type: "text", text: "tail\n" },
        ],
        terminate: true,
      };
    },
  });
  pi.registerTool({
    name: "prepared_tool",
    description: "Transform legacy arguments before execution",
    parameters: { type: "object", required: ["text"], properties: { text: { type: "string" } } },
    prepareArguments(params) { return { text: params.legacy }; },
    async execute(_id, params) { return { content: [{ type: "text", text: `prepared:${params.text ?? ""}` }] }; },
  });
  // Reports the order in which calls start: the number of calls that started before it, plus its own argument.
  let startedCalls = 0;
  pi.registerTool({
    name: "start_order",
    description: "Report the order in which calls start",
    parameters: { type: "object", properties: { n: { type: "number" } } },
    async execute(_id, params) { startedCalls += 1; return { content: [{ type: "text", text: `start#${startedCalls} n=${params.n}` }] }; },
  });
  pi.registerTool({
    name: "tool_error",
    description: "Return a thrown tool error",
    parameters: { type: "object" },
    async execute() { throw new Error("tool exploded"); },
  });
  pi.registerTool({
    name: "tool_is_error",
    description: "Return a structured tool error result",
    parameters: { type: "object" },
    async execute() { return { content: [{ type: "text", text: "soft tool error" }], isError: true }; },
  });
  pi.registerTool({
    name: "guided_tool",
    description: "Tool with prompt guidelines",
    parameters: { type: "object" },
    promptSnippet: " \ufeffGuided\r\n tool\t summary ",
    promptGuidelines: ["Use guided_tool when the user asks for guided behavior."],
    async execute() { return { content: [{ type: "text", text: "guided" }] }; },
  });
  pi.registerTool({
    name: "sourced_tool",
    description: "Tool with explicit source",
    parameters: { type: "object" },
    promptGuidelines: ["Use sourced_tool to test per-tool source attribution."],
    source: "mcp:test-server",
    async execute() { return { content: [{ type: "text", text: "sourced" }] }; },
  });
  pi.registerTool({name: "sampling_disabled", label: "sampling_disabled", description: "Disable constrained sampling", parameters: {type: "object"}, constrainedSampling: false, async execute() { return {content:[{type:"text",text:"disabled"}]}; }});
  pi.registerTool({
    name: "grammar_tool",
    description: "Tool with a grammar constrained sampling request",
    parameters: { type: "object" },
    constrainedSampling: { type: "grammar", variants: { openai_lark: "start: NUMBER" } },
    async execute() { return { content: [{ type: "text", text: "grammar" }] }; },
  });

  pi.registerCommand("complete_probe", {
    description: "Complete its arguments",
    getArgumentCompletions: (prefix) => {
      const items = [{ value: "alpha", label: "alpha — first" }, { value: "apple", description: "fruit" }, { value: "beta" }]
        .filter((item) => item.value.startsWith(prefix.trim()));
      return items.length > 0 ? items : null;
    },
    handler: async () => {},
  });
  pi.registerCommand("ping", { description: "Respond with pong", handler: async (_args, ctx) => ctx.ui.notify("pong", "info") });
  pi.registerCommand("model-stream-probe", {
    description: "Exercise model streaming",
    handler: async (_args, ctx) => {
      const current = ctx.modelRegistry.find("conformance", "current");
      if (!current || current.id !== "current") throw new Error(`find current = ${JSON.stringify(current)}`);
      const found = ctx.modelRegistry.find("conformance", "declared");
      if (!found || found.id !== "declared" || found.provider !== "conformance") throw new Error(`find declared = ${JSON.stringify(found)}`);
      const slash = ctx.modelRegistry.find("conformance", "org/model/name");
      if (!slash || slash.id !== "org/model/name") throw new Error(`find slash = ${JSON.stringify(slash)}`);
      const expectedLimits = {"maxRequestBytes":12345,"images":{"maxPerMessage":7,"maxPerRequest":11,"resize":{"maxWidth":321,"maxHeight":123,"maxBytes":45678,"jpegQuality":67}}};
      const checkLimits = (actual, expected) => Object.entries(expected).every(([key, value]) => typeof value === "object" ? actual?.[key] && checkLimits(actual[key], value) : actual?.[key] === value);
      if (!checkLimits(slash.inputLimits, expectedLimits) || !checkLimits(ctx.model?.inputLimits, expectedLimits)) throw new Error(`model inputLimits = ${JSON.stringify(slash)}`);
      for (const field of ["baseUrl", "input", "cost", "thinkingLevelMap", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"]) {
        if (!(field in slash)) throw new Error(`find slash missing ${field}: ${JSON.stringify(slash)}`);
      }
      if (slash.input.length !== 0 || slash.cost.input !== 0 || slash.cost.tiers.length !== 1 || slash.compat.supportsStrictMode !== false) throw new Error(`find slash shape = ${JSON.stringify(slash)}`);
      if (ctx.modelRegistry.find("conformance", "missing") !== undefined) throw new Error("find missing returned a model");
      if (ctx.modelRegistry.find("conformance", "override-only") !== undefined) throw new Error("find override-only returned a model");
      const auth = await ctx.modelRegistry.getApiKeyAndHeaders(found);
      if (Object.keys(auth).length !== 5 || auth.ok !== true || auth.apiKey !== "conformance-key" || auth.headers?.["X-Conformance-Auth"] !== "yes" || auth.baseUrl !== "https://models.invalid/v1" || auth.env?.CONFORMANCE_AUTH !== "yes") throw new Error(`auth = ${JSON.stringify(auth)}`);
      const model = { provider: "conformance", id: "declared", modelId: "declared", api: "openai-responses" };
      const request = {
        systemPrompt: "conformance-system",
        messages: [
          { role: "system", content: [{ type: "text", text: "signed system", textSignature: "system-signature" }], sections: { zeta: "last-first", alpha: null, middle: "middle" }, timestamp: 41 },
          { role: "user", content: "hello", timestamp: 42 },
          { role: "assistant", content: [{ type: "text", text: "prior", textSignature: "signed" }], api: "openai-responses", provider: "prior-provider", model: "prior-model", usage: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4, totalTokens: 10, cost: { input: 0.1, output: 0.2, cacheRead: 0.3, cacheWrite: 0.4, total: 1.0 } }, stopReason: "stop", timestamp: 43 },
        ],
        tools: [{ name: "lookup", description: "lookup", parameters: { type: "object" }, constrainedSampling: { type: "grammar", variants: { openai_lark: "start: NUMBER" } } }],
      };
      const options = {
        timeoutMs:0,websocketConnectTimeoutMs:1234,maxRetries:2,maxRetryDelayMs:3000,
        maxTokens: 321, temperature: 0.65, samplingParams: { topP: 0.8 },
        thinkingBudgets: { minimal: 11, low: 22, medium: 33, high: 44 }, reasoning: "high", isReasoning: true,
        env: { WIRE_ENV: "request-value", SECOND_ENV: "distinct-value" }, headers: { "X-Wire": "yes", "X-Remove": null }, sessionId: "conformance-session", transport: "sse",
      };
      const stream = ctx.modelRegistry.stream(model, request, options);
      const types = [];
      for await (const event of stream) types.push(event.type);
      if (types.join(",") !== "start,text_start,text_delta,text_end,done") throw new Error(`stream events = ${types}`);
      const result = await stream.result();
      if (result.content?.[0]?.text !== "streamed") throw new Error(`stream result = ${JSON.stringify(result)}`);
      const simple = ctx.modelRegistry.streamSimple(model, request, options);
      const simpleTypes = [];
      for await (const event of simple) simpleTypes.push(event.type);
      if (simpleTypes.join(",") !== "start,text_start,text_delta,text_end,done") throw new Error(`simple events = ${simpleTypes}`);
      if ((await simple.result()).stopReason !== "stop") throw new Error("simple did not stop");
      if ((await ctx.modelRegistry.complete(model, request, options)).stopReason !== "stop") throw new Error("complete did not stop");
      if ((await ctx.modelRegistry.stream(model, request, options).result()).stopReason !== "stop") throw new Error("result without iteration did not stop");
      const unknown = await ctx.modelRegistry.complete({ provider: "conformance", id: "unknown", modelId: "unknown", api: "openai-responses" }, request, options);
      if (unknown.stopReason !== "error" || !unknown.errorMessage?.includes("unknown model")) throw new Error(`unknown result = ${JSON.stringify(unknown)}`);
      const transportError = await ctx.modelRegistry.complete({ provider: "conformance", id: "protocol-error", modelId: "protocol-error", api: "openai-responses" }, request, options);
      if (!Number.isInteger(transportError.timestamp) || transportError.timestamp <= 0) throw new Error(`transport timestamp = ${transportError.timestamp}`);
      const { timestamp: _timestamp, ...transportWithoutTimestamp } = transportError;
      const expectedTransport = {
        role: "assistant", content: [], api: "openai-responses", provider: "conformance", model: "protocol-error",
        usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
        stopReason: "error", errorMessage: "transport boom",
      };
      if (JSON.stringify(transportWithoutTimestamp) !== JSON.stringify(expectedTransport)) throw new Error(`transport result = ${JSON.stringify(transportWithoutTimestamp)}`);
      ctx.ui.notify("model-stream=ok", "info");
    },
  });
  pi.registerCommand("model-stream-callback-probe", {
    description: "Exercise the provider request callbacks of modelRegistry.stream",
    handler: async (_args, ctx) => {
      const model = { provider: "conformance", id: "declared", modelId: "declared", api: "openai-responses" };
      const request = { systemPrompt: "callbacks", messages: [{ role: "user", content: "hello", timestamp: 1 }] };
      const seen = {};
      const options = {
        onPayload: (payload, callbackModel) => { seen.payload = payload; seen.model = callbackModel?.id; return { ...payload, mark: "on-payload" }; },
        onResponse: (response, callbackModel) => { seen.response = response; },
        transformHeaders: (headers) => ({ ...headers, "x-transformed": "yes" }),
      };
      const stream = ctx.modelRegistry.stream(model, request, options);
      for await (const event of stream) void event;
      if ((await stream.result()).stopReason !== "stop") throw new Error("callback stream did not stop");
      if (seen.payload?.original !== true || seen.model !== "declared") throw new Error(`onPayload saw ${JSON.stringify(seen)}`);
      if (seen.response?.status !== 201 || seen.response?.headers?.["x-upstream"] !== "seen") throw new Error(`onResponse saw ${JSON.stringify(seen.response)}`);
      if ((await ctx.modelRegistry.stream(model, request, {}).result()).stopReason !== "stop") throw new Error("plain stream did not stop");
      ctx.ui.notify("model-callbacks=ok", "info");
    },
  });
  pi.registerCommand("overlay-handle-probe", {
    description: "Exercise the overlay handle ui.custom hands to onHandle",
    handler: async (_args, ctx) => {
      const failures = [];
      const expect = (label, actual, expected) => { if (JSON.stringify(actual) !== JSON.stringify(expected)) failures.push(`${label}: ${JSON.stringify(actual)} != ${JSON.stringify(expected)}`); };
      const onHandle = (handle) => {
        expect("initial focused", handle.isFocused(), true);
        expect("initial hidden", handle.isHidden(), false);
        expect("initial bounds", handle.getBounds(), { row: 3, col: 4, width: 20, height: 5 });
        handle.focus();
        expect("focus", handle.isFocused(), true);
        handle.setHidden(true);
        expect("hidden", [handle.isHidden(), handle.isFocused(), handle.getBounds()], [true, false, undefined]);
        handle.setHidden(false);
        expect("shown", [handle.isHidden(), handle.isFocused()], [false, false]);
        handle.focus();
        expect("refocus", handle.isFocused(), true);
        handle.unfocus();
        expect("unfocus", handle.isFocused(), false);
        handle.unfocus({ target: null });
      };
      const result = await ctx.ui.custom((_tui, _theme, _keys, done) => ({ render: () => ["overlay"], handleInput: (data) => { if (data === "q") done("closed"); }, invalidate() {} }), { overlay: true, onHandle });
      void result;
      if (failures.length) throw new Error(failures.join("; "));
      ctx.ui.notify("overlay-handle=ok", "info");
    },
  });
  pi.registerCommand("model-stream-fetch-probe", {
    description: "Exercise the fetch option of modelRegistry.stream",
    handler: async (_args, ctx) => {
      const model = { provider: "conformance", id: "declared", modelId: "declared", api: "openai-responses" };
      const request = { systemPrompt: "fetch", messages: [{ role: "user", content: "hello", timestamp: 1 }] };
      const seen = {};
      const fetch = async (url, init) => {
        seen.url = String(url);
        seen.method = init.method;
        seen.host = new Headers(init.headers).get("x-host");
        seen.body = Buffer.from(init.body).toString();
        return new Response(Uint8Array.from({ length: 70000 }, (_, index) => index % 251), { status: 207, statusText: "Answered", headers: { "x-sdk-fetch": "answered" } });
      };
      const stream = ctx.modelRegistry.stream(model, request, { fetch });
      for await (const event of stream) void event;
      if ((await stream.result()).stopReason !== "stop") throw new Error("fetch stream did not stop");
      if (seen.url !== "https://fetch.invalid/v1/chat?x=1" || seen.method !== "POST" || seen.host !== "1" || seen.body !== "ping-body") throw new Error(`fetch saw ${JSON.stringify(seen)}`);
      if ((await ctx.modelRegistry.stream(model, request, {}).result()).stopReason !== "stop") throw new Error("plain stream did not stop");
      ctx.ui.notify("model-fetch=ok", "info");
    },
  });
  pi.registerCommand("liveness_host_call", { description: "Exercise an awaited host call", handler: async (_args, ctx) => ctx.waitForIdle() });
  pi.registerCommand("liveness_user_call", { description: "Exercise an interactive host call", handler: async (_args, ctx) => ctx.ui.input("Question", "Answer") });
  pi.registerCommand("liveness_fire_call", { description: "Exercise a no-result UI host call", handler: async (_args, ctx) => ctx.ui.setTitle("Conformance title") });
  pi.registerCommand("command_error", { description: "Return a command error", handler: async () => { throw new Error("command exploded"); } });
  pi.registerCommand("command_awaited_error", {
    description: "Return an error after awaited work",
    handler: async () => {
      await new Promise((resolve) => setTimeout(resolve, 150));
      throw new Error("awaited command exploded");
    },
  });
  pi.registerCommand("status", { description: "Set a status entry", handler: async (_args, ctx) => ctx.ui.setStatus("conformance", "ok") });
  pi.registerCommand("status_burst", {
    description: "Set one status repeatedly without awaiting",
    handler: async (_args, ctx) => { for (let i = 0; i < 200; i++) ctx.ui.setStatus("burst", String(i)); },
  });
  // A width handler makes a host call the way any other handler does; the host's reply must reach it (conformance TestConformance_WidthHandlerHostCall).
  let widthProbe;
  pi.registerCommand("arm_width_probe", { description: "Notify from a width handler", handler: async (_args, ctx) => { widthProbe ??= ctx.ui.onWidthChange(async (width) => { await ctx.ui.notify(`width-probe:${width}`, "info"); await ctx.ui.notify(`width-probe-returned:${width}`, "info"); }); } });
  let eventProbeOff;
  pi.registerCommand("event_probe_subscribe", { description: "Subscribe to turn_end after connecting", handler: async () => { eventProbeOff = pi.on("turn_end", async (event, ctx) => { ctx.ui.notify(`event_probe:${event.messageEntryId}`, "info"); }); } });
  pi.registerCommand("event_probe_unsubscribe", { description: "Remove the turn_end handler registered after connecting", handler: async () => { eventProbeOff?.(); eventProbeOff = undefined; } });
  pi.registerCommand("report_geometry", { description: "Report observed terminal geometry", handler: async (_args, ctx) => ctx.ui.notify(`geometry:${ctx.width}x${ctx.height}`, "info") });

  pi.registerCommand("surface_footer", { description: "Install a footer renderer", handler: async (_args, ctx) => ctx.ui.setFooter(() => ({ render: (width) => [`footer@${width}`], invalidate() {} })) });
  pi.registerCommand("surface_header", { description: "Install a header renderer", handler: async (_args, ctx) => ctx.ui.setHeader(() => ({ render: (width) => [`header@${width}`], invalidate() {} })) });
  pi.registerCommand("surface_static_footer", { description: "Push static footer rows", handler: async (_args, ctx) => ctx.ui.setFooter(() => ({ render: () => [`static@${ctx.width}`], invalidate() {} })) });
  pi.registerCommand("surface_widget", { description: "Set a string list widget wider than the pane", handler: async (_args, ctx) => ctx.ui.setWidget("wide", ["A".repeat(60) + " tail", "short"]) });

  let unsubscribeTerminalInput;
  pi.registerCommand("term_subscribe", {
    description: "Subscribe to raw terminal input",
    handler: async (_args, ctx) => {
      unsubscribeTerminalInput = ctx.ui.onTerminalInput((data) => {
        if (["\ud83d", "\ude00", "😀"].includes(data)) return { data: "seen:" + data };
        if (data === "\x1b[96~") return { data: JSON.stringify([ctx.ui.getEditorText(), ctx.ui.getToolsExpanded()]) };
        if (data === "\x1b[98~") return { data: "rewritten" };
        if (data === "\x1b[97~") {
          const until = Date.now() + 200;
          while (Date.now() < until) {}
          return { consume: true };
        }
        return { consume: data === "\x1b[99~" };
      });
    },
  });
  pi.registerCommand("term_unsubscribe", {
    description: "Release the raw input subscription",
    handler: async () => { unsubscribeTerminalInput?.(); unsubscribeTerminalInput = undefined; },
  });
  pi.registerCommand("send_message", { description: "Send a custom message", handler: async () => pi.sendMessage({ customType: "notice", content: "hello-custom", display: true }, { triggerTurn: true, deliverAs: "steer" }) });
  pi.registerCommand("send_message_default", { description: "Send a custom message with default options", handler: async () => pi.sendMessage({ customType: "notice", content: "default", display: true }) });
  pi.registerCommand("send_message_no_turn", { description: "Send a custom message that never starts a turn", handler: async () => pi.sendMessage({ customType: "notice", content: "no-turn", display: true }, { triggerTurn: false }) });
  pi.registerCommand("send_user_message", { description: "Send a user message", handler: async (args) => pi.sendUserMessage(args ? JSON.parse(args) : "hello-user", { deliverAs: "followUp" }) });
  pi.registerCommand("set_session_name", { description: "Set the session name", handler: async () => pi.setSessionName("conformance-session") });
  pi.registerCommand("event_result_probe", { description: "Subscribe to an event with a handler that returns the given JSON", handler: async (args) => { const [name, ...rest] = String(args).trim().split(" "); const result = JSON.parse(rest.join(" ")); pi.on(name, async () => result); } });
  pi.registerCommand("event_field_probe", { description: "Report a field of the events named by the argument `<event> <field>`", handler: async (args) => { const [name, field] = String(args).trim().split(/\s+/); pi.on(name, async (event, ctx) => { ctx.ui.notify(`event_field:${name}.${field}=${JSON.stringify(event[field]) ?? "absent"}`, "info"); }); } });
  pi.registerCommand("event_probe_on", { description: "Subscribe to the event named by the argument", handler: async (args) => { const name = String(args).trim(); pi.on(name, async (_event, ctx) => { ctx.ui.notify(`event_probe_on:${name}`, "info"); }); } });
  pi.registerCommand("host_state_probe", {
    description: "Report the thinking level and the session commands",
    handler: async (_args, ctx) => {
      const commands = pi.getCommands().map((c) => [c.name, c.description ?? "", c.source, c.sourceInfo?.path ?? "", c.sourceInfo?.scope ?? ""].join("|"));
      ctx.ui.notify(`getThinkingLevel=${pi.getThinkingLevel()};getCommands=${commands.join(",")}`, "info");
    },
  });
  pi.registerCommand("set_model", {
    description: "Switch to the model in the arguments",
    handler: async (args, ctx) => {
      const slash = args.indexOf("/");
      const ok = await pi.setModel({ provider: args.slice(0, slash), id: args.slice(slash + 1) });
      ctx.ui.notify(`setModel=${ok}`, "info");
    },
  });
  const eventProbeOn = new Map();
  const eventProbeResults = new Map();
  pi.registerCommand("event_probe_result", { description: "Queue the JSON result the next event_probe_on handler call of an event returns", handler: async (args) => { const text = String(args).trim(); const split = text.indexOf(" "); const name = text.slice(0, split); eventProbeResults.set(name, [...(eventProbeResults.get(name) ?? []), JSON.parse(text.slice(split + 1))]); } });
  pi.registerCommand("event_probe_on", { description: "Subscribe to the event named by the argument", handler: async (args) => { const name = String(args).trim(); const off = pi.on(name, async (event, ctx) => { ctx.ui.notify(`event_probe_on:${name}`, "info"); ctx.ui.notify(`event_probe_data:${name}:${JSON.stringify(event)}`, "info"); if (Array.isArray(event?.servers)) ctx.ui.notify(`event_probe_servers:${event.servers.map((server) => server.name).join(",")}`, "info"); ctx.ui.notify(`event_payload:${name}:${JSON.stringify(event, (_key, value) => (value instanceof Set ? [...value].sort() : value))}`, "info"); const queued = (eventProbeResults.get(name) ?? []).shift(); if (name === "before_provider_headers" && queued) { for (const key of Object.keys(event.headers)) delete event.headers[key]; Object.assign(event.headers, queued); return undefined; } return queued; }); eventProbeOn.set(name, [...(eventProbeOn.get(name) ?? []), off]); } });
  pi.registerCommand("event_field_probe", { description: "Report a field of the events named by the argument `<event> <field>`", handler: async (args) => { const [name, field] = String(args).trim().split(/\s+/); pi.on(name, async (event, ctx) => { ctx.ui.notify(`event_field:${name}.${field}=${JSON.stringify(event[field]) ?? "absent"}`, "info"); }); } });
  pi.registerCommand("event_probe_off", { description: "Unsubscribe the event_probe_on handlers of the event named by the argument", handler: async (args) => { const name = String(args).trim(); for (const off of eventProbeOn.get(name) ?? []) off(); eventProbeOn.delete(name); } });
  pi.registerCommand("event_payload_probe", { description: "Subscribe and report the payload of the event named by the argument", handler: async (args) => { const name = String(args).trim(); pi.on(name, async (event, ctx) => { ctx.ui.notify(`event_payload:${name}:${JSON.stringify(event)}`, "info"); }); } });
  pi.registerCommand("settings_probe", { description: "Report the effective settings", handler: async (_args, ctx) => ctx.ui.notify(`settings_probe:${JSON.stringify(pi.getSettings())}`, "info") });
  pi.registerCommand("model_set", { description: "Switch the model", handler: async (args, ctx) => { const [provider, ...rest] = String(args).trim().split("/"); const ok = await pi.setModel({ provider, id: rest.join("/") }); ctx.ui.notify(`model_set:${ok}`, "info"); } });
  pi.registerCommand("thinking_set", { description: "Set the thinking level", handler: async (args) => pi.setThinkingLevel(String(args).trim()) });
  pi.registerCommand("thinking_get", { description: "Report the thinking level", handler: async (_args, ctx) => ctx.ui.notify(`thinking_get:${pi.getThinkingLevel()}`, "info") });
  pi.registerCommand("label_probe", { description: "Label the entry named first", handler: async (args) => { const [id, ...rest] = String(args).trim().split(" "); pi.setLabel(id, rest.join(" ")); } });
  pi.registerCommand("set_label", { description: "Set an entry label", handler: async () => pi.setLabel("label-entry", "conformance-label") });
  pi.registerShortcut("ctrl+alt+y", { description: "Conformance shortcut", handler: async () => {} });
  pi.registerCommand("append_entry", { description: "Append a custom entry", handler: async () => pi.appendEntry("conformance-entry", "hello-entry") });
  pi.registerCommand("scoped-models-probe", { description: "Report the model scope", handler: async (_args, ctx) => ctx.ui.notify(JSON.stringify(ctx.scopedModels), "info") });
  pi.registerCommand("ui-availability", {
    description: "Probe bound UI and headless defaults",
    handler: async (_args, ctx) => {
      const selected = await ctx.ui.select("Pick", ["first", "second"]);
      ctx.ui.notify("availability-notify", "info");
      if (!ctx.hasUI) {
        const fail = () => { throw new Error("headless factory invoked"); };
        if (await ctx.ui.input("Input", "placeholder") !== undefined ||
            await ctx.ui.editor("Editor", "prefill") !== undefined ||
            await ctx.ui.confirm("Confirm", "message") !== false ||
            await ctx.ui.custom(fail) !== undefined) throw new Error("headless dialog result");
        ctx.ui.setWidget("ignored", fail);
        ctx.ui.setFooter(fail);
        ctx.ui.setHeader(fail);
        ctx.ui.setEditorComponent(fail);
        ctx.ui.addAutocompleteProvider(fail);
        const off = ctx.ui.onTerminalInput(fail); off(); off();
        ctx.ui.setEditorText("ignored"); ctx.ui.setToolsExpanded(true);
        if (ctx.ui.getEditorText() !== "" || ctx.ui.getToolsExpanded() ||
            ctx.ui.getAllThemes().length || ctx.ui.getTheme("dark") !== undefined ||
            ctx.ui.setTheme("dark").error !== "UI not available") throw new Error("headless getter result");
      }
      pi.appendEntry("ui-availability", `hasUI=${ctx.hasUI} selected=${selected ?? ""}`);
    },
  });
  pi.registerCommand("ui-state-barrier", { handler: async (_, ctx) => {
    const selected = await ctx.ui.select("expand", ["chosen"]);
    const named = ctx.ui.getTheme("light");
    const missing = ctx.ui.getTheme("missing");
    const result = ctx.ui.setTheme("missing");
    ctx.ui.notify(`selected=${selected} expanded=${ctx.ui.getToolsExpanded()} named=${named?.name} missing=${missing === undefined} success=${result.success} error=${result.error}`, "info");
  }});
  pi.registerCommand("autocomplete-register", {handler: async (_,ctx) => {
    for(const tag of ["A","B"]) {
      ctx.ui.addAutocompleteProvider(current => {
        ctx.ui.notify("factory:"+tag,"info");
        let calls=0;
        return {
          triggerCharacters: tag === "A" ? ["$"] : ["#","$"],
          async getSuggestions(lines,line,col,options) {
            const count=++calls;
            const result=await current.getSuggestions(lines,line,col,options);
            if(result==null)return result;
            return {...result,items:tag === "A" ? result.items.filter(item=>item.value!=="drop").map(item=>({...item,label:`${item.value}:${count}`})) : [...result.items,{value:"tail",label:`tail:${count}`} ]};
          },
          applyCompletion(...args) {
            const result=current.applyCompletion(...args);
            result.lines[result.cursorLine]+="-"+tag;
            result.cursorCol+=2;
            return result;
          },
          shouldTriggerFileCompletion(...args){return current.shouldTriggerFileCompletion(...args);},
        };
      });
      ctx.ui.notify("registered:"+tag,"info");
    }
  }});
  // Upstream ctx.cwd, ctx.mode, ctx.hasUI and ctx.model throw the stale message after invalidation (runner.ts:571-600).
  pi.registerCommand("stale-probe", {
    description: "Report cwd, mode, hasUI and model",
    handler: (_args, ctx) => ctx.ui.notify(`stale:${ctx.cwd}|${ctx.mode}|${ctx.hasUI}|${ctx.model?.id ?? ""}`, "info"),
  });
  pi.registerCommand("signal-probe", {
    description: "Report ctx.signal",
    handler: (_args, ctx) => ctx.ui.notify(`signal:${ctx.signal === undefined ? "none" : ctx.signal.aborted ? "aborted" : "live"}`, "info"),
  });
  pi.registerCommand("signal-wait", {
    description: "Wait for ctx.signal to abort",
    handler: async (_args, ctx) => {
      const signal = ctx.signal;
      if (signal === undefined) return ctx.ui.notify("wait:none", "info");
      ctx.ui.notify("wait:start", "info");
      const outcome = await new Promise((resolve) => {
        const timer = setTimeout(() => resolve("timeout"), 10_000);
        const done = () => { clearTimeout(timer); resolve("aborted"); };
        if (signal.aborted) return done();
        signal.addEventListener("abort", done, { once: true });
      });
      ctx.ui.notify(`wait:${outcome}`, "info");
    },
  });
  // signal-poll reads ctx.signal from timers, as an extension does between requests (runner.ts:917-920): the state is "live" or "none".
  pi.registerCommand("signal-poll", {
    description: "Poll ctx.signal until it is live or none",
    handler: async (args, ctx) => {
      ctx.ui.notify("poll:start", "info");
      const deadline = Date.now() + 10_000;
      while (Date.now() < deadline) {
        if ((ctx.signal !== undefined) === (args === "live")) return ctx.ui.notify(`poll:${args}`, "info");
        await new Promise((resolve) => setTimeout(resolve, 5));
      }
      ctx.ui.notify("poll:timeout", "info");
    },
  });
  pi.registerCommand("usage-probe", {
    handler: (_args, ctx) => ctx.ui.notify(JSON.stringify(ctx.getContextUsage() ?? null), "info"),
  });
  pi.registerCommand("context-probe", {
    description: "Report ctx.mode + ctx.getSystemPromptOptions()",
    handler: async (_args, ctx) => {
      const options = ctx.getSystemPromptOptions();
      const shape = (v) => v === undefined || v === null ? "absent" : Array.isArray(v) ? `array:${v.length}` : v !== null && typeof v === "object" ? `object:${Object.keys(v).length}` : `${typeof v}:${String(v).length}`;
      const shapes = ["selectedTools", "toolSnippets", "toolGuidelines", "promptGuidelines", "appendSystemPrompt", "sections", "contextFiles", "skills"].map((key) => `${key}:${shape(options[key])}`).join(",");
      ctx.ui.notify(`mode=${ctx.mode} trusted=${ctx.isProjectTrusted()} spo_prompt=${options.customPrompt ?? ""} spo_cwd=${options.cwd ?? ""} spo_tools=${(options.selectedTools ?? []).join(",")} spo_shape=${shapes} spo_guidelines=${(options.toolGuidelines?.read ?? []).join(",")} spo_skill_scope=${options.skills?.[0]?.sourceInfo?.scope ?? ""} spo_force_empty=${options.forceSystemPrompt === ""} spo_custom_present=${Object.hasOwn(options, "customPrompt")}`, "info");
    },
  });
  pi.registerCommand("sprite-probe", {
    description: "Exercise sprite registration and host errors",
    handler: async (_args, ctx) => {
      const definition = spriteDefinition();
      await ctx.ui.registerSprite(definition);
      definition.mascot[0] = definition.mascot[0].slice(0, -1);
      try {
        await ctx.ui.registerSprite(definition);
      } catch (error) {
        ctx.ui.notify(error.message, "error");
        return;
      }
      throw new Error("invalid sprite definition was accepted");
    },
  });
  pi.registerCommand("login-probe", {
    description: "Exercise semantic login submission and host errors",
    handler: async (_args, ctx) => {
      const definition = loginDefinition();
      await ctx.ui.setLogin(definition);
      definition.brand[0] = definition.brand[0].slice(0, -1);
      try {
        await ctx.ui.setLogin(definition);
      } catch (error) {
        ctx.ui.notify(error.message, "error");
        return;
      }
      throw new Error("invalid login definition was accepted");
    },
  });
  pi.registerCommand("session-log-probe", {
    description: "Read a paged session log",
    handler: async (_args, ctx) => {
      ctx.ui.notify(`session entries=${ctx.sessionManager.getEntries().length} branch=${ctx.sessionManager.getBranch().length}`, "info");
    },
  });
  pi.registerCommand("dialog-probe", {
    description: "Exercise interactive dialog responses",
    handler: async (_args, ctx) => {
      const selected = await ctx.ui.select("Pick", ["first", "second"]);
      const input = await ctx.ui.input("Input", "placeholder");
      const edited = await ctx.ui.editor("Editor", "prefill");
      const confirmed = await ctx.ui.confirm("Confirm", "message");
      ctx.ui.notify(`select=${selected} input=${input} editor=${edited} confirm=${confirmed}`, "info");
    },
  });

  class FocusedList {
    constructor(done) {
      this.done = done;
      this.items = ["alpha", "beta", "gamma"];
      this.selected = 0;
      this.disposed = false;
    }
    render(width) { return [`focused width=${width}`, ...this.items.map((item, index) => `${index === this.selected ? "> " : "  "}${item}`)]; }
    handleInput(data) {
      if (data === "\x1b[A") this.selected = (this.selected + this.items.length - 1) % this.items.length;
      else if (data === "\x1b[B") this.selected = (this.selected + 1) % this.items.length;
      else if (data === "\x1b[6~") this.selected = Math.min(this.selected + 2, this.items.length - 1);
      else if (data === "\r" || data === "\n") this.done(this.items[this.selected]);
      else if (data === "\x1b") this.done(undefined);
    }
    dispose() { this.disposed = true; }
  }
  pi.registerCommand("focused-probe", {
    description: "Exercise focused subprocess UI",
    handler: async (_args, ctx) => {
      let component;
      const selected = await ctx.ui.custom((_tui, _theme, _keys, done) => (component = new FocusedList(done)), { overlayOptions: { title: "Focused", widthFraction: 0.5, heightFraction: 0.5 } });
      ctx.ui.notify(`focused=${selected ?? ""} disposed=${component.disposed}`, "info");
    },
  });

  // The component kit's conformance probe (D107,
  // docs/plan/extension-component-kit.md §10), built from Pi's components: the
  // runtime derives its view while a frontend draws. As ui.custom's component
  // every key reaches it; it passes the list's keys to the list and logs the
  // rest, and the list's callbacks log its events. It closes on select with
  // the log joined by ",". kit-surfaces and kit-message draw the same tree.
  class KitProbe extends Container {
    constructor(theme, keys, done) {
      super();
      this.keys = keys;
      this.log = [];
      const items = Array.from({ length: 5 }, (_, i) => ({ value: `k${i}`, label: `Track ${i}`, description: `Artist ${i}` }));
      this.list = new SelectList(items, 3, getSelectListTheme());
      this.list.setSelectedIndex(2);
      this.list.onSelectionChange = (item) => this.log.push(`selectionChange:${this.list.selectedIndex}:${item.value}`);
      this.list.onSelect = (item) => {
        this.log.push(`select:${this.list.selectedIndex}:${item.value}`);
        done?.(this.log.join(","));
      };
      this.addChild(new DynamicBorder((s) => theme.fg("accent", s)));
      this.addChild(new Text("Kit probe", 2, 0, (s) => theme.bg("customMessageBg", s)));
      this.addChild(new Markdown("- one\n- **two**", 1, 0, getMarkdownTheme()));
      this.addChild(new HStack([{ component: new TruncatedText("left side"), grow: 1 }, { component: new TruncatedText("right"), grow: 1 }], { gap: 1 }));
      this.addChild(new Spacer(1));
      this.addChild(this.list);
      this.viewTheme = { accent: "#d75f00" };
    }
    handleInput(data) {
      if (["tui.select.up", "tui.select.down", "tui.select.confirm", "tui.select.cancel"].some((id) => this.keys.matches(data, id))) this.list.handleInput(data);
      else this.log.push(`input:${data}`);
    }
  }
  pi.registerCommand("kit-probe", {
    description: "Exercise the component kit (D107)",
    handler: async (_args, ctx) => {
      const result = await ctx.ui.custom((_tui, theme, keys, done) => new KitProbe(theme, keys, done), { overlayOptions: { title: "Kit" } });
      ctx.ui.notify(`kit=${result ?? ""}`, "info");
    },
  });
  // mouse-probe's component: Pi's handleMouse, which logs every event
  // it receives, all of its fields, and closes on a click with the log
  // joined by ",".
  class MouseProbe {
    constructor(done) {
      this.done = done;
      this.log = [];
    }
    render() { return ["mouse probe", "row 1", "row 2", "row 3"]; }
    handleInput() {}
    handleMouse(e) {
      const mods = [[e.shift, "S"], [e.alt, "A"], [e.ctrl, "C"]].filter(([on]) => on).map(([, name]) => name).join("");
      this.log.push(`${e.type}/${e.button}/${e.x},${e.y}/${e.screenX},${e.screenY}/${e.width}x${e.height}/w${e.wheelDelta ?? 0}/c${e.clickCount ?? 0}/${mods}`);
      if (e.type === "click") this.done(this.log.join(","));
      return { handled: true };
    }
  }
  pi.registerCommand("mouse-probe", {
    description: "Exercise extension mouse input",
    handler: async (_args, ctx) => {
      const result = await ctx.ui.custom((_tui, _theme, _keys, done) => new MouseProbe(done), { overlay: true });
      ctx.ui.notify(`mouse=${result ?? ""}`, "info");
    },
  });
  pi.registerMessageRenderer("kit-message", (_message, _options, theme) => new KitProbe(theme));
  pi.registerCommand("kit-surfaces", {
    description: "Show the kit probe on every view surface (D107)",
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("kit-probe", (_tui, theme) => new KitProbe(theme));
      ctx.ui.setHeader((_tui, theme) => new KitProbe(theme));
      ctx.ui.setFooter((_tui, theme) => new KitProbe(theme));
      pi.registerTool({
        name: "kit_view_tool",
        label: "kit_view_tool",
        description: "Render its result as the kit probe",
        parameters: { type: "object", properties: {} },
        async execute() { return { content: [{ type: "text", text: "kit" }] }; },
        renderResult(_result, _options, theme) { return new KitProbe(theme); },
      });
    },
  });
  // kitImagePNG is image n of kit-images: a 1×1 PNG whose pixel encodes n, so
  // each n has its own bytes and ref.
  const kitImagePNG = (n) => {
    const chunk = (type, data) => {
      const body = Buffer.concat([Buffer.from(type, "latin1"), data]);
      const out = Buffer.alloc(body.length + 8);
      out.writeUInt32BE(data.length, 0);
      body.copy(out, 4);
      out.writeUInt32BE(zlib.crc32(body), body.length + 4);
      return out;
    };
    const header = Buffer.from([0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0]);
    const pixel = zlib.deflateSync(Buffer.from([0, n & 0xff, (n >> 8) & 0xff, 0x5f, 0xff]));
    return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", header), chunk("IDAT", pixel), chunk("IEND", Buffer.alloc(0))]).toString("base64");
  };
  // kit-images drives the image transport (D107, spec §7): step "a<k>"
  // shows image 0 and step "b<n>" image n (1 ≤ n ≤ 64), each with its step
  // as the text, as one ui.setWidget call on the "kit-img" widget.
  pi.registerCommand("kit-images", {
    description: "Exercise the component kit's image transport (D107)",
    handler: async (args, ctx) => {
      const step = /^([ab])(\d+)$/.exec(args);
      const n = Number(step?.[2]);
      if (!step || n < 1 || (step[1] === "b" && n > 64)) throw new Error(`kit-images: unknown step ${JSON.stringify(args)}`);
      const image = step[1] === "a" ? 0 : n;
      ctx.ui.setWidget("kit-img", () => {
        const c = new Container();
        c.addChild(new Image(kitImagePNG(image), "image/png", { fallbackColor: (s) => s }));
        c.addChild(new Text(args, 0, 0));
        return c;
      });
    },
  });
  // kit-kinds sets the "kit-kinds" widget to the kinds kit-probe does not
  // draw (D107, spec §10), from Pi's components: a box, a settings list, a
  // loader, an image and a component with its own render whose viewLines
  // list goes out only while a frontend draws.
  const kitKindsPNG = Buffer.from("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c48900000010494441547801010500faff002a005fff026a01892888e8cd0000000049454e44ae426082", "hex").toString("base64");
  pi.registerCommand("kit-kinds", {
    description: "Show every other component kit kind (D107)",
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("kit-kinds", (tui, theme) => {
        const box = new Box(1, 0, (s) => theme.bg("customMessageBg", s));
        box.addChild(new Text("boxed", 0, 0));
        // getSettingsListTheme() draws the cursor once, when it is called;
        // a getter draws it at render time, under the surface's viewTheme.
        const settingsTheme = Object.defineProperty({ ...getSettingsListTheme() }, "cursor", { get: () => getSettingsListTheme().cursor, enumerable: true });
        const settings = new SettingsList([
          { id: "theme", label: "Theme", description: "Color theme", currentValue: "dark", values: ["dark", "light"] },
          { id: "wrap", label: "Wrap", description: "Wrap lines", currentValue: "on", values: ["on", "off"] },
        ], 3, settingsTheme, () => {}, () => {});
        const loader = new Loader(tui, (s) => theme.fg("accent", s), (s) => theme.fg("muted", s), "Working", { frames: ["*"] });
        const tracks = {
          render: () => ["track one", "track two"],
          invalidate() {},
          viewLines: { list: { items: [{ label: "track one", detail: "A" }, { label: "track two", detail: "B" }], selectedIndex: 1 } },
        };
        const stack = new VStack([box, settings, loader, new Image(kitKindsPNG, "image/png", { fallbackColor: (s) => s }), tracks], { gap: 1 });
        stack.viewTheme = { accent: "#d75f00" };
        return stack;
      });
    },
  });
  // kit-conversation sets the "kit-conversation" widget to Pi's
  // conversation components (D107, spec §2.1, §10); "next" updates the
  // components the last frame kept (the streaming reply, the ls and grep
  // cards and the ls -la command), as a Pi author updates kept components.
  let kitKept;
  pi.registerCommand("kit-conversation", {
    description: "Show Pi's conversation components (D107)",
    handler: async (args, ctx) => {
      const next = args === "next";
      ctx.ui.setWidget("kit-conversation", (tui) => {
        const reply = (more) => ({ content: [{ type: "thinking", thinking: "Reading the *kit* file" }, { type: "text", text: `Done. **Bold** reply\n\n1. a\n2. b${more ? "\n\nThen more kit." : ""}` }] });
        const card = (name, callId, toolArgs, definition) => {
          const tool = new ToolExecutionComponent(name, callId, toolArgs, {}, definition, tui, "/work/kit");
          tool.setArgsComplete();
          tool.markExecutionStarted();
          return tool;
        };
        if (!next || !kitKept) {
          const streaming = new AssistantMessageComponent();
          streaming.updateContent(reply(false), true);
          const ls = card("ls", "call-1", { path: "src" }, createLsToolDefinition("/work/kit"));
          ls.updateResult({ content: [{ type: "text", text: "a.go\nb.go" }] }, false);
          const grep = card("grep", "call-2", { pattern: "TODO" }, createGrepToolDefinition("/work/kit"));
          grep.updateResult({ content: [{ type: "text", text: "x.go:1: TODO kit" }] }, true);
          const exited = new BashExecutionComponent("ls -la", tui, false);
          exited.appendOutput("a.txt\n");
          exited.appendOutput("b.txt");
          exited.setComplete(2, false);
          kitKept = { streaming, ls, grep, exited };
        }
        const { streaming, ls, grep, exited } = kitKept;
        if (next) {
          streaming.updateContent(reply(true), false);
          grep.updateResult({ content: [{ type: "text", text: "x.go:1: TODO kit\ny.go:2: TODO kit" }] }, false);
          exited.setExpanded(true);
        }
        const user = new UserMessageComponent("Fix **the** kit build\n\n- one\n- two");
        const failed = new AssistantMessageComponent({ content: [{ type: "thinking", thinking: "secret" }, { type: "text", text: "Visible kit" }], stopReason: "error", errorMessage: "kit-boom-7" });
        failed.setHideThinkingBlock(true);
        failed.setHiddenThinkingLabel("Pondering kit...");
        failed.setOutputPad(0);
        const read = card("read", "call-3", { path: "missing.txt" }, createReadToolDefinition("/work/kit"));
        read.updateResult({ content: [{ type: "text", text: "ENOENT: kit" }], isError: true }, false);
        read.setExpanded(true);
        const custom = card("kit_tool", "call-4", { q: "x" }, {});
        custom.updateResult({ content: [{ type: "text", text: "answer 42" }] }, false);
        const seq = new BashExecutionComponent("seq 25", tui, true);
        seq.appendOutput(Array.from({ length: 25 }, (_, i) => String(i + 1)).join("\n"));
        seq.setComplete(0, false);
        const diff = new Text(renderDiff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail", { filePath: "kit.go" }), 0, 0);
        const root = new Container();
        for (const child of [user, streaming, failed, ls, grep, read, custom, exited, seq, diff]) root.addChild(child);
        return root;
      });
    },
  });

  class TimerFocused {
    constructor(tui, done) {
      this.done = done;
      this.frame = 0;
      this.disposed = false;
      this.detached = false;
      this.timer = setInterval(() => { this.frame++; tui.requestRender(); }, 20);
    }
    render(width) { return [`timer frame=${this.frame} width=${width}`]; }
    handleInput(data) { if (data === "\r") this.done(this.frame); }
    dispose() { clearInterval(this.timer); this.disposed = true; this.detached = true; }
  }
  pi.registerCommand("overlay-width-probe", {
    description: "Report the width overlay components render at",
    handler: async (_args, ctx) => {
      const probe = (_tui, _theme, _keys, done) => {
        let width = 0;
        return { render(w) { width = w; return [`overlay width=${w}`]; }, invalidate() {}, handleInput(data) { if (data === "\r") done(width); } };
      };
      const defaultWidth = await ctx.ui.custom(probe, { overlay: true });
      const percentWidth = await ctx.ui.custom(probe, { overlay: true, overlayOptions: { width: "50%" } });
      ctx.ui.notify(`overlay-width default=${defaultWidth} percent=${percentWidth}`, "info");
    },
  });
  pi.registerCommand("timer-focused-probe", {
    description: "Exercise timer-driven focused UI",
    handler: async (_args, ctx) => {
      let component;
      const frame = await ctx.ui.custom((tui, _theme, _keys, done) => (component = new TimerFocused(tui, done)), { overlayOptions: { title: "Timer" } });
      ctx.ui.notify(`timer=${frame ?? 0} disposed=${component.disposed} detached=${component.detached}`, "info");
    },
  });

  // Typed tool events (upstream PowerShellToolCallEvent/BashToolCallEvent and
  // their result variants): record the input and details, block the
  // conformance sentinel command, and replace a result's content.
  pi.on("user_bash", event => {
    const result = { output: "handled", exitCode: 7, cancelled: false, truncated: false };
    const value = { result };
    switch (event.command) {
      case "valid": break;
      case "undefined": result.exitCode = undefined; break;
      case "undefined-path": result.fullOutputPath = undefined; break;
      case "missing": delete result.exitCode; break;
      case "invalid": result.exitCode = "invalid"; break;
      case "null-operations": value.operations = null; break;
      case "operations": return { operations: conformanceBashOperations() };
      default: return undefined;
    }
    return value;
  });

  pi.on("tool_call", async (event, ctx) => {
    // Pi's handler mutates event.input in place and the runner reads it back (runner.ts emitToolCall).
    // Assigning a new object to event.input leaves the object the tool runs with (agent-loop.ts prepareToolCall).
    if (event.toolName === "rewrite_reassign_probe") {
      event.input = { ...event.input, command: "git status --short" };
      return;
    }
    // Moving a member to the end is an edit in Pi, and two added members follow in the order the handler added them.
    if (event.toolName === "rewrite_order_probe") {
      if (event.input.command === "reorder") {
        const timeout = event.input.timeout;
        delete event.input.timeout;
        event.input.timeout = timeout;
      } else {
        event.input.zeta = 1;
        event.input.alpha = 2;
      }
      return;
    }
    if (event.toolName === "rewrite_probe" || event.toolName === "rewrite_block_probe") {
      if (event.input.command === "git status" || event.toolName === "rewrite_block_probe") {
        event.input.command = "git status --short";
        delete event.input.drop;
        event.input.added = true;
        event.input.nested = { depth: 2 };
      }
      if (event.toolName === "rewrite_block_probe") return { block: true, reason: "blocked after rewrite" };
      return;
    }
    if (event.toolName !== "powershell" && event.toolName !== "bash") return;
    ctx.ui.notify(`tool-call=${event.toolName}:${event.input?.command}:${event.input?.timeout}`, "info");
    if (event.input?.command === "blocked-command") return { block: true, reason: `blocked ${event.toolName}` };
  });
  pi.on("tool_result", async (event, ctx) => {
    if (event.toolName !== "powershell" && event.toolName !== "bash") return;
    ctx.ui.notify(`tool-result=${event.toolName}:${event.details?.fullOutputPath}:${event.details?.truncation?.totalLines}:${event.content?.[0]?.text}`, "info");
    return { content: [{ type: "text", text: `${event.toolName} redacted` }] };
  });
  pi.on("after_provider_response", async (event, ctx) => {
    await Promise.resolve();
    ctx.ui.notify(`provider-response=${event.type}:${event.status}:${event.headers["x-probe"]}`, "info");
    return { cancel: true };
  });
  pi.on("after_provider_response", (_event, ctx) => ctx.ui.notify("provider-response=second", "info"));
  pi.on("message_update", async (event, ctx) => {
    const assistant = event.assistantMessageEvent ?? {};
    ctx.ui.notify(`message-update=${assistant.type}:${assistant.contentIndex}:${assistant.delta}:${Object.hasOwn(assistant, "assistantMessageEvent")}`, "info");
  });
  pi.on("tool_execution_update", async (event, ctx) => {
    if (event.toolName === "production_tool") {
      ctx.ui.notify(`tool-update=${event.toolName}:${event.args?.path}:${event.args?.nested?.depth}:${event.partialResult?.content?.[0]?.text}:${event.partialResult?.details?.progress}`, "info");
      return;
    }
    ctx.ui.notify(`tool-update=${event.toolName}:${JSON.stringify(event.args)}:${event.partialResult?.content?.[0]?.text}:${event.partialResult?.details?.progress}`, "info");
  });
  pi.on("tool_execution_end", async (event, ctx) => {
    if (event.toolName === "production_tool") {
      ctx.ui.notify(`tool-end=${event.toolName}:${event.result?.content?.[0]?.text}:${event.result?.content?.length}:${event.result?.content?.[1]?.data}:${event.result?.content?.[1]?.mimeType}:${event.result?.details?.nested?.value}:${event.isError}`, "info");
      return;
    }
    ctx.ui.notify(`tool-end=${event.toolName}:${event.result?.content?.length}:${event.result?.content?.[1]?.data}:${event.result?.details?.nested?.value}:${event.isError}`, "info");
  });
  pi.on("project_trust", async () => { throw new Error("trust-boom"); });
  pi.on("project_trust", async () => ({ trusted: "undecided" }));
  pi.on("project_trust", async (event) => (event.cwd === "/probe" ? { trusted: "undecided" } : { trusted: "yes", remember: true }));
  pi.on("cache_warming_decision", async (event) => {
    if (event.warmCost !== 0.05 || event.missCost !== 0.5 || event.continuationProbability !== 0.15 || event.action !== "warm") throw new Error("unexpected cache decision");
    return { action: "stop" };
  });
  pi.on("agent_before_settle", async (event, ctx) => {
    ctx.ui.notify(`agent_before_settle:${event.outcome}:${event.entries.length}:${Boolean(event.continue)}:${event.context.contextEntries.length}:${Boolean(event.context.canContinue)}`, "info");
    event.entries.push({ type: "custom", customType: "kept" });
  });
  pi.on("agent_before_settle", (event) => {
    event.entries.push({ type: "custom", customType: "before-error" });
    throw new Error("boundary failed");
  });
  pi.on("agent_before_settle", (event) => {
    if (event.entries.length !== 2 || event.context.contextEntries.length !== 2 || event.entries[0].customType !== "kept" || event.entries[1].customType !== "before-error") {
      throw new Error("lost boundary mutation or preview");
    }
    return { entries: [{ type: "custom", customType: "conformance-boundary" }], continue: true };
  });
  pi.on("session_start", async (event, ctx) => {
    ctx.ui.notify(`session_start:${event.reason ?? ""}`, "info");
    if (event.previousSessionFile) ctx.ui.notify(`previous:${event.previousSessionFile}`, "info");
  });
  pi.on("session_shutdown", async (event, ctx) => {
    ctx.ui.notify(`session_shutdown:${event.reason ?? ""}`, "info");
    if (event.targetSessionFile) ctx.ui.notify(`target:${event.targetSessionFile}`, "info");
  });
  pi.on("session_info_changed", async (event, ctx) => ctx.ui.notify(`session_info_changed:${event.name ?? ""}`, "info"));
  pi.on("session_before_compact", async (event, ctx) => ctx.ui.notify(`session_before_compact:${event.reason ?? ""}:${Boolean(event.willRetry)}`, "info"));
  pi.on("session_compact", async (event, ctx) => ctx.ui.notify(`session_compact:${event.reason ?? ""}:${Boolean(event.willRetry)}:${Boolean(event.fromExtension)}`, "info"));
  pi.on("session_compact_failed", async (event, ctx) => ctx.ui.notify(`session_compact_failed:${event.reason ?? ""}:${event.errorMessage ?? ""}:${Boolean(event.aborted)}:${Boolean(event.willRetry)}:${Boolean(event.fromExtension)}`, "info"));
  pi.on("turn_end", event => {
    if (event.messageEntryId !== "boundary-assistant") return;
    event.entries.push({type:"custom", customType:"mutated"});
    throw new Error("turn-boundary-failure");
  });
  pi.on("turn_end", event => {
    if (event.messageEntryId !== "boundary-assistant") return;
    return {entries:[{type:"custom", customType:"turn-boundary", data:structuredClone(event)}], continue:true};
  });
  pi.on("turn_end", async (event, ctx) => ctx.ui.notify(`turn_end:${event.messageEntryId}:${event.toolResultEntryIds[0]}`, "info"));
  let promptSequence = 0;
  for (const name of ["ui_prompt_start", "ui_prompt_end"]) {
    pi.on(name, (event, ctx) => {
      const sequence = promptSequence++;
      if (event.title?.startsWith("fifo:")) {
        ctx.ui.notify(`fifo:${sequence}:${event.type}:${event.title}`, "info");
        return;
      }
      ctx.ui.notify(`ui_prompt:${event.type}:${event.reason}:${event.kind}:${"title" in event ? event.title : "(none)"}`, "info");
    });
  }
}

// The BashOperations every SDK fixture returns for the user_bash command "operations" (TestConformance_UserBashOperationsRunInTheExtension).
function conformanceBashOperations() {
  return {
    exec: async (command, cwd, { onData, signal, timeout, env }) => {
      switch (command) {
        case "echo":
          onData(Buffer.from(`cmd:${command}\n`));
          onData(Buffer.from(`cwd:${cwd}\n`));
          if (env !== undefined && Object.keys(env).length === 0) onData(Buffer.from("env-empty\n"));
          for (const name of Object.keys(env ?? {}).sort()) onData(Buffer.from(`env:${name}=${env[name]}\n`));
          if (timeout !== undefined) onData(Buffer.from(`timeout:${timeout}\n`));
          return { exitCode: 3 };
        case "chunks":
          for (const chunk of ["a", "b", "c"]) onData(Buffer.from(chunk));
          return { exitCode: null };
        case "binary":
          onData(Buffer.from([0xff, 0x00, 0x80]));
          return { exitCode: 0 };
        case "wait":
          onData(Buffer.from("waiting"));
          await new Promise((resolve) => (signal.aborted ? resolve() : signal.addEventListener("abort", resolve, { once: true })));
          onData(Buffer.from("stopped"));
          throw new Error("aborted");
        default:
          throw new Error(`exec failed: ${command}`);
      }
    },
  };
}
