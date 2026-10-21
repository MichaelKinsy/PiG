import fs from "node:fs";
import zlib from "node:zlib";
import { existsSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import * as piAi from "@earendil-works/pi-ai";
import { join as joinPath } from "node:path";
import * as piTui from "@earendil-works/pi-tui";
import * as piCodingAgent from "@earendil-works/pi-coding-agent";
const { Container } = piTui;
import {
  createBashToolDefinition, createGrepToolDefinition, createLsToolDefinition, createReadToolDefinition, CONFIG_DIR_NAME, isToolCallEventType, CustomEditor, getAgentDir, getPackageDir, getShellConfig, generateDiffString, generateUnifiedPatch, sessionEntryToContextMessages, convertToPng, resizeImage, readStoredCredential, VERSION, formatSize, DEFAULT_MAX_LINES, DEFAULT_MAX_BYTES, truncateHead, truncateLine, truncateTail, withFileMutationQueue, isBashToolResult, isEditToolResult, isFindToolResult, isGrepToolResult, isLsToolResult, isPowerShellToolResult, isReadToolResult, isWriteToolResult,
  getMarkdownTheme, getSelectListTheme, getSettingsListTheme, renderDiff,
} from "@earendil-works/pi-coding-agent";

export default function (pi) {
  // factoryAgentDir is getAgentDir() while the factory runs, where Pi's extensions call it (TestConformance_HostConfigurationAgreesAcrossSDKs).
  const factoryAgentDir = getAgentDir();
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
  pi.registerCommand("exec-abort-probe", { description: "Abort a running exec through its signal", handler: async (args, ctx) => {
    const probe = JSON.parse(args);
    const controller = new AbortController();
    if (probe.pre) controller.abort();
    else {
      void (async () => {
        for (const deadline = Date.now() + 15000; Date.now() < deadline && !existsSync(probe.pidFile);) await new Promise((resolve) => setTimeout(resolve, 10));
        controller.abort();
      })();
    }
    try {
      const result = await pi.exec(probe.command, probe.args, { signal: controller.signal });
      ctx.ui.notify(`exec-abort:killed=${result.killed};code=${result.code};stdout=${result.stdout.trim()}`, "info");
    } catch (error) {
      ctx.ui.notify(`rejected:${error.message}`, "info");
    }
  }});
  pi.registerCommand("thinking-model", { description: "Read the thinking level and switch the model", handler: async (_args, ctx) => {
    pi.setThinkingLevel("high");
    const ok = await pi.setModel({ provider: "probe", id: "model" });
    ctx.ui.notify(JSON.stringify([pi.getThinkingLevel(), ok]), "info");
  }});
  pi.registerCommand("replicated-accessors", { description: "Read the replicated accessors after each write", handler: async (_args, ctx) => {
    const lastType = () => { const entries = ctx.sessionManager.getEntries(); return entries[entries.length - 1]?.type ?? ""; };
    const out = { name0: ctx.sessionManager.getSessionName() ?? "", model0: ctx.model?.id ?? "", provider0: ctx.model?.provider ?? "" };
    pi.setSessionName("renamed");
    out.name1 = ctx.sessionManager.getSessionName() ?? "";
    pi.setLabel("one", "lab");
    out.entryAfterLabel = lastType();
    pi.sendMessage({ customType: "probe", content: "hi", display: true });
    out.entryAfterMessage = lastType();
    await pi.setModel({ provider: "prov-after", id: "id-after" });
    out.model1 = ctx.model?.id ?? "";
    out.provider1 = ctx.model?.provider ?? "";
    out.qualified1 = `${out.provider1}/${out.model1}`;
    ctx.ui.notify(JSON.stringify(out), "info");
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
      persisted:s.isPersisted(), usesDefaultDir:s.usesDefaultSessionDir(), children:s.getChildren("one"), sessionContext:s.buildSessionContext(),
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
  pi.registerCommand("registry-edges", { description: "Read the registry answers that depend on the provider state", handler: async (_args, ctx) => {
    const r = ctx.modelRegistry;
    const models = [...r.getAll(), {}];
    ctx.ui.notify(JSON.stringify({
      available: r.getAvailable(), hasAuth: models.map((model) => r.hasConfiguredAuth(model)), oauth: models.map((model) => r.isUsingOAuth(model)),
      found: r.find("registry-probe", "a") ?? null, foundOff: r.find("registry-off", "d") ?? null,
      missing: r.find("registry-probe", "zzz") ?? null, mismatch: r.find("registry-probe", "c") ?? null,
    }), "info");
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
    name: "big_out",
    description: "Return text of the size in the argument",
    parameters: { type: "object", properties: {} },
    async execute(_id, params) {
      return { content: [{ type: "text", text: "a".repeat(params.n) }] };
    },
  });
  pi.registerTool({
    name: "echo_params",
    description: "Return the arguments as the result details",
    parameters: { type: "object", properties: {} },
    async execute(_id, params) {
      return { content: [{ type: "text", text: "echoed" }], details: params };
    },
  });
  pi.registerTool({
    name: "nan_out",
    description: "Return details holding NaN and Infinity",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: "nan" }], details: { n: NaN, i: -Infinity, l: [NaN, 2, { d: Infinity }], ok: 1 } };
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
  // Notifies inline (before the handler returns, and from a microtask and a promise continuation) and from a setImmediate and a setTimeout(0) callback; the callbacks run once the handler returned (D189).
  let staleCtx;
  pi.registerTool({
    name: "timer_defer",
    description: "Notify inline and from deferred callbacks",
    parameters: { type: "object" },
    async execute(_id, _params, _signal, _onUpdate, ctx) {
      staleCtx = ctx;
      ctx.ui.notify("inline:sync", "info");
      queueMicrotask(() => ctx.ui.notify("inline:microtask", "info"));
      Promise.resolve().then(() => ctx.ui.notify("inline:promise", "info"));
      process.nextTick(() => ctx.ui.notify("tick:nextTick", "info"));
      setImmediate(() => ctx.ui.notify("deferred:immediate", "info"));
      setTimeout(() => ctx.ui.notify("deferred:timeout", "info"), 0);
      return { content: [{ type: "text", text: "queued" }] };
    },
  });
  // Awaits a host call made through the context of the earlier timer_defer request, from a timer callback it waits for (Pi-port extensions keep such a context); the call must not wait for a loop turn that waits for this handler.
  pi.registerTool({
    name: "timer_stale",
    description: "Await a host call made through an earlier request's context",
    parameters: { type: "object" },
    async execute() {
      await new Promise((resolve) => setTimeout(() => { staleCtx.ui.notify("stale:awaited", "info"); resolve(); }, 0));
      return { content: [{ type: "text", text: "applied" }] };
    },
  });
  pi.registerTool({
    name: "timer_release",
    description: "Nothing: Node's callbacks need no release",
    parameters: { type: "object" },
    async execute() { return { content: [{ type: "text", text: "released" }] }; },
  });
  // order_probe makes the host calls and updates whose order the ordering rows pin (ordering_test.go): its first acts are two synchronous host calls, then one update, and the tool_result handler below reports each result as its first act.
  pi.registerTool({
    name: "order_probe",
    description: "Make a host call and an update in the first tick",
    parameters: { type: "object", properties: { n: { type: "number" } } },
    async execute(_id, params, _signal, onUpdate, ctx) {
      ctx.ui.notify(`order-call=${params.n}`, "info");
      ctx.ui.notify(`order-then=${params.n}`, "info");
      onUpdate({ content: [{ type: "text", text: `order-update=${params.n}` }] });
      return { content: [{ type: "text", text: `order-done n=${params.n}` }] };
    },
  });
  // mutation_order_probe takes the file mutation queue in its first statement (late: "loop" awaits the event loop first) and appends its tag to the file (TestFileMutationOrderAcrossSDKs).
  pi.registerTool({
    name: "mutation_order_probe",
    description: "Take the file mutation queue and append a tag to the file",
    parameters: { type: "object", properties: { file: { type: "string" }, tag: { type: "string" }, late: { type: "string" } } },
    async execute(_id, params) {
      if (params.late === "loop") await new Promise((resolve) => setImmediate(resolve));
      await withFileMutationQueue(params.file, async () => {
        const before = fs.existsSync(params.file) ? fs.readFileSync(params.file, "utf8") : "";
        fs.writeFileSync(params.file, `${before}${params.tag}\n`);
      });
      return { content: [{ type: "text", text: `mutated ${params.tag}` }] };
    },
  });
  pi.registerTool({
    name: "dialog_order",
    description: "Open a select dialog titled by the call and report the answer",
    parameters: { type: "object", properties: { title: { type: "string" }, exec: { type: "boolean" } } },
    async execute(_id, params, _signal, _onUpdate, ctx) {
      if (params.exec === true) await pi.exec("dialog-order-exec", []).catch(() => {});
      const selected = await ctx.ui.select(params.title, ["a", "b"]);
      return { content: [{ type: "text", text: `dialog:${params.title}=${selected ?? ""}` }] };
    },
  });
  pi.registerTool({
    name: "throw_probe",
    description: "Throw a value of the requested kind",
    parameters: { type: "object", properties: { kind: { type: "string" } } },
    async execute(_id, params) {
      switch (params.kind) {
        case "unicode": throw new Error("é😀 \n x");
        case "empty": throw new Error("");
        case "long": throw new Error("x".repeat(100000));
      }
      return { content: [{ type: "text", text: "ok" }] };
    },
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
  pi.registerCommand("overlay-focus-probe", {
    description: "Render the focus the host publishes to a mounted overlay",
    handler: async (_args, ctx) => {
      let handle;
      await ctx.ui.custom((_tui, _theme, _keys, done) => ({
        render: () => [`focused=${handle ? handle.isFocused() : "pending"}`],
        handleInput: (data) => { if (data === "q") done("closed"); },
        invalidate() {},
      }), { overlay: true, onHandle: (value) => { handle = value; } });
      ctx.ui.notify("overlay-focus=ok", "info");
    },
  });
  // Pi hands a ui.custom factory its TUI: tui.showOverlay shows a raw panel whose handle works inside the factory (tui.ts:729-826).
  pi.registerCommand("overlay-show-probe", {
    description: "Show and focus a raw overlay from a ui.custom factory's tui",
    handler: async (_args, ctx) => {
      const result = await ctx.ui.custom((tui, _theme, _keys, done) => {
        const keys = [];
        let panel;
        const child = {
          focused: false,
          render: () => [`panel focused=${child.focused} keys=${keys.join("")}`],
          handleInput: (data) => {
            keys.push(data);
            if (data === "x") { panel.hide(); done(`closed:${panel.isFocused()}`); }
          },
          invalidate() {},
        };
        panel = tui.showOverlay(child, { nonCapturing: true, width: 30 });
        panel.focus();
        if (!panel.isFocused() || !child.focused) throw new Error("the panel's focus did not apply inside the factory");
        return { render: () => ["owner"], handleInput() {}, invalidate() {} };
      }, { overlay: true });
      ctx.ui.notify(`overlay-show=${result}`, "info");
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
  pi.registerCommand("surface_static_header", { description: "Push static header rows", handler: async (_args, ctx) => ctx.ui.setHeader(() => ({ render: () => [`static@${ctx.width}`], invalidate() {} })) });
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
  // rw_prompt_state registers two tools and activates one, reading the system prompt state after each write.
  pi.registerCommand("rw_prompt_state", {
    description: "Read the system prompt state after registering and activating tools",
    handler: async (_args, ctx) => {
      const tool = (name) => ({ name, label: name, description: name, promptSnippet: `${name}-snippet`, parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
      pi.registerTool(tool("rw-alpha"));
      pi.registerTool(tool("rw-beta"));
      const registered = ctx.getSystemPromptOptions();
      pi.setActiveTools(["rw-beta"]);
      const options = ctx.getSystemPromptOptions();
      const prompt = ctx.getSystemPrompt();
      ctx.ui.notify(`rw_prompt_state=registered:${registered.toolSnippets?.["rw-alpha"] ?? ""};selected:${(options.selectedTools ?? []).join(",")};beta:${prompt.includes("rw-beta-snippet")};alpha:${prompt.includes("rw-alpha-snippet")}`, "info");
    },
  });
  pi.registerCommand("rw_idle_after_trigger", {
    description: "Read isIdle after a message that starts a run",
    handler: async (_args, ctx) => {
      pi.sendMessage({ customType: "rw-trigger", content: "rw-trigger", display: true }, { triggerTurn: true });
      ctx.ui.notify(`rw_idle_after_trigger=${ctx.isIdle()}`, "info");
    },
  });
  let rwSettledOnce = false;
  pi.registerCommand("rw_idle_in_settled", {
    description: "Read isIdle after a message that starts a run, sent from an agent_settled handler",
    handler: async () => {
      pi.on("agent_settled", async (_event, ctx) => {
        if (rwSettledOnce) return;
        rwSettledOnce = true;
        pi.sendMessage({ customType: "rw-trigger", content: "rw-trigger", display: true }, { triggerTurn: true });
        ctx.ui.notify(`rw_idle_in_settled=${ctx.isIdle()}`, "info");
      });
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
  pi.registerCommand("event_probe_off", { description: "Unsubscribe the event_probe_on handlers of the event named by the argument", handler: async (args) => { const name = String(args).trim(); for (const off of eventProbeOn.get(name) ?? []) off(); eventProbeOn.delete(name); } });
  // A JavaScript Set (session_before_compact preparation.fileOps) has no JSON form; the other SDKs receive the host's array, so report the Set as its members.
  pi.registerCommand("event_payload_probe", { description: "Subscribe and report the payload of the event named by the argument", handler: async (args) => { const name = String(args).trim(); pi.on(name, async (event, ctx) => { ctx.ui.notify(`event_payload:${name}:${JSON.stringify(event, (_key, value) => (value instanceof Set ? [...value] : value))}`, "info"); }); } });
  pi.registerCommand("tool_result_guards_probe", { description: "Report the tool_result guards that accept each tool_result event", handler: async () => { const guards = { bash: isBashToolResult, edit: isEditToolResult, find: isFindToolResult, grep: isGrepToolResult, ls: isLsToolResult, powershell: isPowerShellToolResult, read: isReadToolResult, write: isWriteToolResult }; pi.on("tool_result", async (event, ctx) => { ctx.ui.notify(`tool_result_guards:${Object.keys(guards).filter((name) => guards[name](event)).join(",")}:end`, "info"); }); } });
  pi.registerCommand("language_probe", { description: "Report the highlight language of a path", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    ctx.ui.notify(`language:${JSON.stringify({ lang: piCodingAgent.getLanguageFromPath(c.path) ?? null })}:end`, "info");
  } });
  pi.registerCommand("convert_probe", { description: "Run convertToLlm or formatDimensionNote on a JSON case and report the result", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    const result = c.fn === "convert" ? piCodingAgent.convertToLlm(c.messages) : piCodingAgent.formatDimensionNote(c.image) ?? null;
    ctx.ui.notify(`convert:${JSON.stringify({ result })}:end`, "info");
  } });
  pi.registerCommand("serialize_probe", { description: "Run serializeConversation on JSON messages and report the text", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    let out;
    try { out = { result: piCodingAgent.serializeConversation(c.messages) }; } catch { out = { error: true }; }
    ctx.ui.notify(`serialize:${JSON.stringify(out)}:end`, "info");
  } });
  // branch_probe runs prepareBranchEntries or collectEntriesForBranchSummary (TestConformance_BranchSummaryHelpersAgreeAcrossSDKs); collect reads a session built from the entries.
  pi.registerCommand("branch_probe", { description: "Run a branch summarization helper on a JSON case and report the result", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    let out;
    try {
      if (c.fn === "prepare") {
        const r = piCodingAgent.prepareBranchEntries(c.entries, c.budget);
        out = { messages: r.messages, fileOps: { read: [...r.fileOps.read], written: [...r.fileOps.written], edited: [...r.fileOps.edited] }, totalTokens: r.totalTokens };
      } else {
        const byId = new Map(c.entries.map((e) => [e.id, e]));
        const session = {
          getEntry: (id) => byId.get(id),
          getBranch: (fromId) => { const path = []; let cur = fromId === undefined ? undefined : byId.get(fromId); while (cur) { path.unshift(cur); cur = cur.parentId ? byId.get(cur.parentId) : undefined; } return path; },
        };
        const r = piCodingAgent.collectEntriesForBranchSummary(session, c.old, c.target);
        out = { entries: r.entries, commonAncestorId: r.commonAncestorId };
      }
    } catch { out = { error: true }; }
    ctx.ui.notify(`branch:${JSON.stringify(out)}:end`, "info");
  } });
  const keyLists = (value, out = []) => {
    if (Array.isArray(value)) value.forEach((element) => keyLists(element, out));
    else if (value !== null && typeof value === "object") { out.push(Object.keys(value).join(",")); Object.values(value).forEach((element) => keyLists(element, out)); }
    return out;
  };
  const helperReport = (fn, input) => {
    try {
      if (fn === "date") { const ms = Date.parse(input); return { ms: Number.isNaN(ms) ? null : ms }; }
      if (fn === "strip") return { body: piCodingAgent.stripFrontmatter(input) };
      const parsed = piCodingAgent.parseFrontmatter(input);
      return { frontmatter: parsed.frontmatter, body: parsed.body, keys: keyLists(parsed.frontmatter) };
    } catch { return { error: true }; }
  };
  const factoryHelpers = { frontmatter: helperReport("frontmatter", "---\nz: 1\n10: a\n2: b\nnested:\n  y: 1\n  3: x\n  \"q r\": [1, 2]\n---\nbody text\n"), error: helperReport("frontmatter", "---\na: [\n---\nx"), date: helperReport("date", "2020-01-02T03:04:05.678Z"), strip: helperReport("strip", "---\nz: 1\n---\nbody text\n") };
  pi.registerCommand("helper_probe", { description: "Run parseFrontmatter, stripFrontmatter or Date.parse on its argument", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    ctx.ui.notify(`helper:${JSON.stringify(helperReport(c.fn, c.input))}:end`, "info");
  } });
  pi.registerCommand("helper_factory_probe", { description: "Report the helpers' results from the factory", handler: async (args, ctx) => {
    ctx.ui.notify(`helper-factory:${JSON.stringify(factoryHelpers)}:end`, "info");
  } });
  pi.registerCommand("compact_usage_probe", { description: "Run a compaction usage helper on a JSON case and report the result", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    const pick = (u) => ({ input: u.input ?? 0, output: u.output ?? 0, cacheRead: u.cacheRead ?? 0, cacheWrite: u.cacheWrite ?? 0, totalTokens: u.totalTokens ?? 0 });
    let result;
    if (c.fn === "calc") result = { tokens: piCodingAgent.calculateContextTokens(c.usage) };
    else if (c.fn === "should") result = { result: piCodingAgent.shouldCompact(c.tokens, c.window, c.settings) };
    else if (c.fn === "last") { const u = piCodingAgent.getLastAssistantUsage(c.entries); result = { usage: u ? pick(u) : null }; }
    else if (c.fn === "cut") result = piCodingAgent.findCutPoint(c.entries, c.start, c.end, c.keep);
    else if (c.fn === "turn") result = { index: piCodingAgent.findTurnStartIndex(c.entries, c.index, c.start) };
    else if (c.fn === "estimate") result = { tokens: piCodingAgent.estimateTokens(c.message) };
    else result = piCodingAgent.DEFAULT_COMPACTION_SETTINGS;
    ctx.ui.notify(`compact:${JSON.stringify(result)}:end`, "info");
  } });
  pi.registerCommand("skills_probe", { description: "Run a session or skill helper on a JSON case and report the result", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    let result;
    if (c.fn === "latest") result = { entry: piCodingAgent.getLatestCompactionEntry(c.entries) };
    else if (c.fn === "parse") { const b = piCodingAgent.parseSkillBlock(c.text); result = { block: b ? { name: b.name, location: b.location, content: b.content, userMessage: b.userMessage ?? null } : null }; }
    else if (c.fn === "format") result = { text: c.tool == null ? piCodingAgent.formatSkillsForPrompt(c.skills) : piCodingAgent.formatSkillsForPrompt(c.skills, c.tool) };
    else result = { version: piCodingAgent.CURRENT_SESSION_VERSION, virtual: piCodingAgent.VIRTUAL_MODEL_STATE_ENTRY };
    ctx.ui.notify(`skills:${JSON.stringify(result)}:end`, "info");
  } });
  pi.registerCommand("truncate_probe", { description: "Run a truncation helper on a JSON case and report the result", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    const options = { maxLines: c.maxLines ?? undefined, maxBytes: c.maxBytes ?? undefined };
    let result;
    if (c.fn === "head") result = truncateHead(c.content, options);
    else if (c.fn === "tail") result = truncateTail(c.content, options);
    else if (c.fn === "line") { const r = c.maxChars === undefined ? truncateLine(c.content) : truncateLine(c.content, c.maxChars); result = { text: r.text, wasTruncated: r.wasTruncated }; }
    else if (c.fn === "limits") result = { maxLines: DEFAULT_MAX_LINES, maxBytes: DEFAULT_MAX_BYTES };
    else result = { text: formatSize(c.bytes) };
    ctx.ui.notify(`truncate:${JSON.stringify(result)}:end`, "info");
  } });
  // builtin_probe: see the Go fixture (testfixture/builtin_tool.go). Pi's own create<Tool>ToolDefinition, create<Tool>Tool and createLocalBashOperations are the reference.
  const builtinReport = (result) => {
    const report = { content: result.content.map((block) => block.type === "text" ? { type: "text", text: block.text } : { type: block.type, mimeType: block.mimeType }), isError: !!result.isError };
    if (result.details !== undefined && result.details !== null) { report.details = result.details; report.detailsJSON = JSON.stringify(result.details); }
    if (result.structuredContent && typeof result.structuredContent === "object") { report.structuredKeys = Object.keys(result.structuredContent); report.exitCode = result.structuredContent.exit_code; }
    return report;
  };
  const builtinOptions = (c, log) => {
    const hook = c.hook ? (spawn) => { log.push(`spawnHook:${spawn.command}`); return { command: "printf '%s|%s' \"$HOOK_VAR\" \"$PI_SESSION_ID\"", cwd: spawn.cwd, env: { ...spawn.env, HOOK_VAR: "hook-env" } }; } : undefined;
    const custom = c.ops === "custom";
    switch (c.tool) {
      case "read": return { autoResizeImages: c.autoResizeImages ?? undefined, operations: custom ? { readFile: async (p) => { log.push(`readFile:${p}`); return Buffer.from(`OPS:${p}`); }, access: async (p) => { log.push(`access:${p}`); } } : undefined };
      case "write": return { operations: custom ? { writeFile: async (p, content) => { log.push(`writeFile:${p}:${content}`); }, mkdir: async (d) => { log.push(`mkdir:${d}`); } } : undefined };
      case "edit": return { operations: custom ? { readFile: async (p) => { log.push(`readFile:${p}`); return Buffer.from("hello world\n"); }, writeFile: async (p, content) => { log.push(`writeFile:${p}:${content}`); }, access: async (p) => { log.push(`access:${p}`); } } : undefined };
      case "ls": return { operations: custom ? { exists: async (p) => { log.push(`exists:${p}`); return true; }, stat: async (p) => { log.push(`stat:${p}`); return { isDirectory: () => !p.includes(".") }; }, readdir: async (p) => { log.push(`readdir:${p}`); return ["b.txt", "a.txt", "sub"]; } } : undefined };
      case "grep": return { operations: custom ? { isDirectory: async (p) => { log.push(`isDirectory:${p}`); throw new Error("no such path for the probe"); } } : undefined };
      case "find": return { operations: custom ? { exists: async (p) => { log.push(`exists:${p}`); return false; }, glob: async (pattern) => { log.push(`glob:${pattern}`); return []; } } : undefined };
      case "powershell": return { exposeSessionEnvironment: c.exposeSessionEnvironment ?? undefined };
      default: return { commandPrefix: c.commandPrefix || undefined, exposeSessionEnvironment: c.exposeSessionEnvironment ?? undefined, spawnHook: hook, operations: custom ? { exec: async (command, cwd, o) => { log.push(`exec:${command}|cwd=${cwd}`); o.onData(Buffer.from(`custom:${command}`)); return { exitCode: 0 }; } } : undefined };
    }
  };
  const builtinFactories = { read: [piCodingAgent.createReadToolDefinition, piCodingAgent.createReadTool], write: [piCodingAgent.createWriteToolDefinition, piCodingAgent.createWriteTool], edit: [piCodingAgent.createEditToolDefinition, piCodingAgent.createEditTool], bash: [piCodingAgent.createBashToolDefinition, piCodingAgent.createBashTool], grep: [piCodingAgent.createGrepToolDefinition, piCodingAgent.createGrepTool], find: [piCodingAgent.createFindToolDefinition, piCodingAgent.createFindTool], ls: [piCodingAgent.createLsToolDefinition, piCodingAgent.createLsTool], powershell: [piCodingAgent.createPowerShellToolDefinition, piCodingAgent.createPowerShellTool] };
  pi.registerCommand("builtin_probe", { description: "Delegate to a built-in tool through the SDK and report what it returned", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    const emit = (report) => ctx.ui.notify(`builtin:${JSON.stringify(report)}:end`, "info");
    if (c.kind === "cwd") return emit({ cwd: ctx.cwd });
    if (c.kind === "register") {
      const read = createReadToolDefinition(c.factoryCwd);
      pi.registerTool({ ...read, async execute(...a) { const r = await read.execute(...a); r.content = r.content.map((b, i) => i === 0 ? { ...b, text: `«delegated:${b.text}»` } : b); r.details = { delegated: true }; return r; } });
      const bash = createBashToolDefinition(c.factoryCwd);
      pi.registerTool({ ...bash, async execute(...a) { const r = await bash.execute(...a); r.content = r.content.map((b, i) => i === 0 ? { ...b, text: `«${b.text}»` } : b); return r; } });
      return emit({ registered: [read.name, bash.name] });
    }
    if (c.kind === "sets") return emit({ coding: piCodingAgent.createCodingTools(c.factoryCwd).map((t) => t.name), readOnly: piCodingAgent.createReadOnlyTools(c.factoryCwd).map((t) => t.name) });
    if (c.kind === "local") {
      const operations = c.shell === "powershell" ? piCodingAgent.createLocalPowerShellOperations() : piCodingAgent.createLocalBashOperations();
      const controller = new AbortController();
      const chunks = [];
      const report = {};
      try {
        const result = await operations.exec(c.command, c.factoryCwd, { onData: (data) => { chunks.push(Buffer.from(data).toString("utf8")); if (c.abortOnOutput) controller.abort(); }, signal: controller.signal, timeout: c.timeout ?? undefined, env: c.env ?? undefined });
        report.exitCode = result.exitCode;
      } catch (error) { report.error = error.message; }
      report.output = chunks.join("");
      return emit(report);
    }
    const log = [];
    const [createDefinition, createTool] = builtinFactories[c.tool];
    const options = builtinOptions(c, log);
    if (c.kind === "definition") {
      const def = createDefinition(c.factoryCwd, options);
      return emit({ definition: { name: def.name, label: def.label, description: def.description, promptSnippet: def.promptSnippet ?? "", promptGuidelines: def.promptGuidelines ?? null, parameters: JSON.parse(JSON.stringify(def.parameters)), outputSchema: def.outputSchema ? JSON.parse(JSON.stringify(def.outputSchema)) : null, constrainedSampling: def.constrainedSampling ?? null, executionMode: def.executionMode ?? "", renderShell: def.renderShell ?? "" } });
    }
    const controller = new AbortController();
    if (c.preAborted) controller.abort();
    const updates = [];
    const onUpdate = c.updates || c.abortOnOutput ? (partial) => { const text = partial.content.filter((b) => b.type === "text").map((b) => b.text).join("\n"); updates.push(text); if (c.abortOnOutput && text !== "") controller.abort(); } : undefined;
    const report = {};
    try {
      const result = c.noContext ? await createTool(c.factoryCwd, options).execute("probe-call", c.params ?? {}, controller.signal, onUpdate) : await createDefinition(c.factoryCwd, options).execute("probe-call", c.params ?? {}, controller.signal, onUpdate, ctx);
      report.result = builtinReport(result);
    } catch (error) { report.error = error.message; }
    if (c.updates) report.updates = updates;
    report.calls = log;
    emit(report);
  } });
  // message_probe: see the Go fixture.
  pi.registerCommand("message_probe", { description: "Report messageRole and messageText of a JSON event payload", handler: async (args, ctx) => {
    const data = JSON.parse(args);
    ctx.ui.notify(`message:${JSON.stringify({ role: pi.messageRole(data), text: pi.messageText(data) })}:end`, "info");
  } });
  // file_queue_probe: see the Go fixture; tasks wait on each other's progress, never on a sleep race.
  pi.registerCommand("file_queue_probe", { description: "Run tasks through the file mutation queue and report the log", handler: async (args, ctx) => {
    const c = JSON.parse(args);
    const log = [], failed = [], progress = new Set();
    const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
    const awaitStage = async (kind, ids, who) => {
      for (const other of ids ?? []) {
        const deadline = Date.now() + 20000;
        while (!progress.has(`${kind}:${other}`)) {
          if (Date.now() > deadline) { log.push(`timeout:${who}:${kind}:${other}`); return; }
          await sleep(1);
        }
      }
    };
    const settle = async (ids) => { if ((ids ?? []).length > 0) await sleep(c.settleMs); };
    await Promise.all(c.tasks.map(async (task) => {
      await awaitStage("started", task.waitStarted, task.id);
      await awaitStage("called", task.waitCalled, task.id);
      await settle(task.waitCalled);
      progress.add(`called:${task.id}`);
      try {
        await withFileMutationQueue(task.path, async () => {
          log.push(`start:${task.id}`);
          progress.add(`started:${task.id}`);
          await awaitStage("ended", task.holdEnded, task.id);
          await awaitStage("called", task.holdCalled, task.id);
          await settle(task.holdCalled);
          log.push(`end:${task.id}`);
          progress.add(`ended:${task.id}`);
          if (task.fail) throw new Error("task failed");
        });
      } catch { progress.add(`ended:${task.id}`); failed.push(task.id); }
    }));
    ctx.ui.notify(`file_queue:${JSON.stringify({ log, errors: failed.sort((a, b) => a - b) })}:end`, "info");
  } });
  // host_paths_probe reports getPackageDir() and getShellConfig() as the runtime answers them, for the default shell and for the existing and the missing custom shell path
  // in args ("<existing>|<missing>") (TestConformance_HostPathsAgreeAcrossSDKs).
  pi.registerCommand("host_paths_probe", { description: "Report getPackageDir and getShellConfig", handler: async (args, ctx) => {
    const [existing, missing] = args.split("|");
    const shell = (...custom) => {
      try {
        const config = getShellConfig(...custom);
        return { shell: config.shell, args: config.args, ...(config.commandTransport ? { commandTransport: config.commandTransport } : {}) };
      } catch (error) { return { error: error.message }; }
    };
    ctx.ui.notify(`host_paths:${JSON.stringify({ packageDir: getPackageDir(), default: shell(), custom: shell(existing), missing: shell(missing) })}:end`, "info");
  } });
  // host_helpers_probe runs the pi helpers over the golden file's inputs through Pi's own code (TestConformance_HostHelpersAgreeAcrossSDKs). args is
  // "<golden>|<auth.json path>|<text to copy>". Pi's copyToClipboard runs in this process and writes the system clipboard, so it is not called here.
  pi.registerCommand("host_helpers_probe", { description: "Run the pi helpers over the golden inputs", handler: async (args, ctx) => {
    const [goldenPath, authPath] = args.split("|", 3);
    const golden = JSON.parse(readFileSync(goldenPath, "utf8"));
    const diffs = golden.diffs.map((c) => {
      const lines = c.contextLines === undefined ? [] : [c.contextLines];
      const report = { patch: generateUnifiedPatch(c.path ?? "src/a.ts", c.oldContent, c.newContent, ...lines) };
      if (!c.patchOnly) { const diff = generateDiffString(c.oldContent, c.newContent, ...lines); report.diff = diff.diff; report.firstChangedLine = diff.firstChangedLine ?? null; }
      return report;
    });
    const entries = golden.entries.map((c) => sessionEntryToContextMessages(c.entry));
    const meta = (image) => (image ? { mimeType: image.mimeType, ...(image.originalWidth === undefined ? {} : { originalWidth: image.originalWidth, originalHeight: image.originalHeight, width: image.width, height: image.height, wasResized: image.wasResized }), decodes: image.data.length > 0 } : null);
    const images = [];
    for (const c of golden.images) {
      if (c.op === "convert") images.push(meta(await convertToPng(c.data, c.mimeType)));
      else { try { images.push(meta(await resizeImage(new Uint8Array(Buffer.from(c.data, "base64")), c.mimeType, c.options))); } catch { images.push(null); } }
    }
    const read = (provider, path) => readStoredCredential(provider, path) ?? null;
    const creds = { explicit: read("anthropic", authPath), default: read("anthropic"), absentProvider: read("nobody", authPath), missingFile: read("anthropic", `${authPath}.missing`), notJSON: read("anthropic", `${authPath}.bad`) };
    ctx.ui.notify(`host_helpers:${JSON.stringify({ diffs, entries, images, creds })}:end`, "info");
  } });
  // pi_ai_helpers_probe runs every call of the vector calls (test/parity/sdk-vectors, Pi's own answers at the pin, listed in one file by the test) through the runtime's pi-ai and
  // reports each answer as JSON (TestConformance_PiAiHelpersAgreeAcrossSDKs).
  pi.registerCommand("pi_ai_helpers_probe", { description: "Run the golden pi-ai helper calls", handler: async (args, ctx) => {
    const golden = JSON.parse(readFileSync(args, "utf8"));
    const cases = golden.cases.map((c) => {
      const value = c.fn === "DEFAULT_MAX_AGENT_RETRY_DELAY_MS" ? piAi[c.fn] : piAi[c.fn](...c.args.map((arg) => (arg === null ? undefined : arg)));
      return value === undefined ? [] : [value];
    });
    const signal = new AbortController().signal;
    const auth = [];
    for (const c of golden.auth.cases) {
      const seen = [];
      const result = await piAi.envApiKeyAuth(c.name, c.envVars).resolve({ ctx: { env: async (name) => { seen.push(name); return c.env[name]; } }, credential: c.credential ?? undefined, signal });
      auth.push({ result: result ?? null, seen });
    }
    const prompts = [];
    const credential = await piAi.envApiKeyAuth("Name", ["FIRST", "SECOND"]).login({ signal, prompt: async (request) => { prompts.push(request); return "typed-key"; } });
    const timing = golden.timing.map((c) => {
      const stream = piAi.createAssistantMessageEventStream();
      const message = (isLater) => ({ role: "assistant", content: [], stopReason: c.kind === "error" ? "error" : "stop", ...(isLater || c.timestamp === "now" ? { timestamp: Date.now() } : c.timestamp === "old" ? { timestamp: 1 } : {}), ...(c.preset !== null && !isLater ? { durationMs: c.preset } : {}) });
      const final = message(false);
      const later = message(true);
      if (c.kind === "done") stream.push({ type: "done", reason: "stop", message: final });
      else if (c.kind === "error") stream.push({ type: "error", reason: "error", error: final });
      else if (c.kind === "end") stream.end(final);
      else { stream.push({ type: "done", reason: "stop", message: final }); stream.end(later); }
      return { present: final.durationMs !== undefined, value: c.preset !== null && final.durationMs === c.preset ? c.preset : null, laterTimed: later.durationMs !== undefined };
    });
    ctx.ui.notify(`pi_ai_helpers:${JSON.stringify({ cases, timing, auth, login: { prompts, result: credential } })}:end`, "info");
  } });
  pi.registerCommand("host_info_probe", { description: "Report getAgentDir, CONFIG_DIR_NAME and VERSION", handler: async (_args, ctx) => {
    ctx.ui.notify(`host_info:${JSON.stringify({ agentDir: getAgentDir(), configDirName: CONFIG_DIR_NAME, version: VERSION, factoryAgentDir })}:end`, "info");
  } });
  // ui_dialog_values_probe reports the value each dialog resolves with (TestConformance_UIDialogValuesAgreeAcrossSDKs): `<undefined>` for Pi's undefined, else the JSON of the value.
  pi.registerCommand("ui_dialog_values_probe", { description: "Report the typed result of each dialog", handler: async (_args, ctx) => {
    const report = (name, value) => ctx.ui.notify(`${name}=${value === undefined ? "<undefined>" : JSON.stringify(value)}`, "info");
    for (const title of ["S1", "S2", "S3", "S4"]) report(title, await ctx.ui.select(title, ["a", "b"]));
    for (const title of ["C1", "C2", "C3", "C4"]) report(title, await ctx.ui.confirm(title, "m"));
    for (const title of ["I1", "I2"]) report(title, await ctx.ui.input(title, "p"));
    for (const title of ["E1", "E2"]) report(title, await ctx.ui.editor(title, "p"));
  } });
  pi.registerCommand("ui_forward_probe", { description: "Call the working, paste, editor-text and tools-expanded UI members", handler: async (_args, ctx) => {
    ctx.ui.setWorkingMessage("Thinking hard");
    ctx.ui.setWorkingMessage("");
    ctx.ui.setWorkingMessage(undefined);
    ctx.ui.setHiddenThinkingLabel("Pondering");
    ctx.ui.setHiddenThinkingLabel("");
    ctx.ui.setHiddenThinkingLabel(undefined);
    ctx.ui.setWorkingVisible(false);
    ctx.ui.setWorkingIndicator({ frames: ["-", "+"], intervalMs: 120 });
    ctx.ui.pasteToEditor("pasted \u{1F600}");
    ctx.ui.setEditorText("draft");
    ctx.ui.setToolsExpanded(true);
  } });
  // dialog-stack-probe opens its dialogs at the same time and reports each one's answer as it settles (TestConformance_ConcurrentDialogsAgreeAcrossSDKs). "pair" is a select A and a confirm B; "mix" is a select S, a confirm C, an input I and an editor E.
  pi.registerCommand("dialog-stack-probe", { description: "Open concurrent dialogs from one command", handler: async (scenario, ctx) => {
    const settled = (name) => (value) => ctx.ui.notify(`stack settled ${name}=${value ?? "cancelled"}`, "info");
    const dialogs = scenario === "mix"
      ? [ctx.ui.select("S", ["s1", "s2"]).then(settled("S")), ctx.ui.confirm("C", "message").then(settled("C")), ctx.ui.input("I", "placeholder").then(settled("I")), ctx.ui.editor("E", "prefill").then(settled("E"))]
      : [ctx.ui.select("A", ["a1", "a2"]).then(settled("A")), ctx.ui.confirm("B", "message").then(settled("B"))];
    await Promise.all(dialogs);
    ctx.ui.notify("stack done", "info");
  } });
  // ui_wire_empties_probe calls the UI members Pi's RPC mode writes with empty strings and lists, and the optional members it omits, in this SDK's own spelling of each (TestConformance_UIEmptyMembersAgreeAcrossSDKs).
  pi.registerCommand("ui_wire_empties_probe", { description: "Call the UI members with empty and unset arguments", handler: async (_args, ctx) => {
    ctx.ui.setStatus("k", "");
    ctx.ui.setStatus("k", undefined);
    ctx.ui.notify("");
    ctx.ui.notify("typed", "warning");
    ctx.ui.setTitle("");
    ctx.ui.setEditorText("");
    await ctx.ui.select("T", []);
    await ctx.ui.confirm("", "");
    await ctx.ui.input("T1");
    await ctx.ui.input("T2", "");
    await ctx.ui.input("T3", "ph");
    await ctx.ui.editor("E1");
    await ctx.ui.editor("E2", "");
    await ctx.ui.editor("E3", "pre");
  } });
  pi.registerCommand("tool_call_guard_probe", { description: "Report the tool names the tool_call guard accepts", handler: async () => {
    pi.on("tool_call", async (event, ctx) => { ctx.ui.notify(`tool_call_guard:${["bash", "read", "probe", "other"].filter((name) => isToolCallEventType(name, event)).join(",")}:end`, "info"); });
  } });
  let installedEditorFactory;
  pi.registerCommand("editor-install", { description: "Install a custom editor component", handler: async (_args, ctx) => { installedEditorFactory = (tui, theme, keybindings) => new CustomEditor(tui, theme, keybindings); ctx.ui.setEditorComponent(installedEditorFactory); } });
  pi.registerCommand("editor-get-probe", { description: "Report the factory getEditorComponent returns", handler: async (_args, ctx) => { const got = ctx.ui.getEditorComponent(); ctx.ui.notify(`editor_get:${got === undefined ? "absent" : got === installedEditorFactory ? "installed" : "other"}:end`, "info"); } });
  pi.registerCommand("editor-clear", { description: "Restore the host's editor", handler: async (_args, ctx) => { installedEditorFactory = undefined; ctx.ui.setEditorComponent(undefined); } });
  pi.registerCommand("settings_probe", { description: "Report the effective settings", handler: async (_args, ctx) => ctx.ui.notify(`settings_probe:${JSON.stringify(pi.getSettings())}`, "info") });
  pi.registerCommand("model_set", { description: "Switch the model", handler: async (args, ctx) => { const [provider, ...rest] = String(args).trim().split("/"); const ok = await pi.setModel({ provider, id: rest.join("/") }); ctx.ui.notify(`model_set:${ok}`, "info"); } });
  pi.registerCommand("thinking_set", { description: "Set the thinking level", handler: async (args) => pi.setThinkingLevel(String(args).trim()) });
  pi.registerCommand("thinking_get", { description: "Report the thinking level", handler: async (_args, ctx) => ctx.ui.notify(`thinking_get:${pi.getThinkingLevel()}`, "info") });
  pi.registerCommand("label_probe", { description: "Label the entry named first", handler: async (args) => { const [id, ...rest] = String(args).trim().split(" "); pi.setLabel(id, rest.join(" ")); } });
  pi.registerCommand("set_label", { description: "Set an entry label", handler: async () => pi.setLabel("label-entry", "conformance-label") });
  pi.registerShortcut("ctrl+alt+y", { description: "Conformance shortcut", handler: async () => {} });
  pi.registerCommand("append_entry", { description: "Append a custom entry", handler: async () => pi.appendEntry("conformance-entry", "hello-entry") });
  pi.registerCommand("append-entries-probe", { description: "Append a custom entry for each comma-separated type", handler: async (args) => { for (const customType of String(args).split(",")) pi.appendEntry(customType, `data-of-${customType}`); } });
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
  pi.registerCommand("session-branch-probe", {
    description: "Read the branch, an entry and its label",
    handler: async (args, ctx) => {
      const manager = ctx.sessionManager;
      ctx.ui.notify(`branch=${manager.getBranch().map((entry) => entry.id).join(">")} leaf=${manager.getLeafEntry()?.id ?? "none"} entry=${manager.getEntry(args)?.id ?? "none"} label=${manager.getLabel(args) ?? "none"}`, "info");
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
      this.list = new piTui.SelectList(items, 3, getSelectListTheme());
      this.list.setSelectedIndex(2);
      this.list.onSelectionChange = (item) => this.log.push(`selectionChange:${this.list.selectedIndex}:${item.value}`);
      this.list.onSelect = (item) => {
        this.log.push(`select:${this.list.selectedIndex}:${item.value}`);
        done?.(this.log.join(","));
      };
      this.addChild(new piCodingAgent.DynamicBorder((s) => theme.fg("accent", s)));
      this.addChild(new piTui.Text("Kit probe", 2, 0, (s) => theme.bg("customMessageBg", s)));
      this.addChild(new piTui.Markdown("- one\n- **two**", 1, 0, getMarkdownTheme()));
      this.addChild(new piTui.HStack([{ component: new piTui.TruncatedText("left side"), grow: 1 }, { component: new piTui.TruncatedText("right"), grow: 1 }], { gap: 1 }));
      this.addChild(new piTui.Spacer(1));
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
  // kit-widget "<case key>": a widget kind's Pi vector case
  // (D107, docs/plan/extension-component-kit.md §10), built from Pi's own
  // components; the runtime derives its view while a frontend draws. As
  // ui.custom's component it takes the keys and mouse events the components
  // leave to the extension and notifies each callback they fire as
  // "kit-widget-event=<type>:<value>". It never closes itself: the harness
  // delivers the case's steps and then reads the callbacks, and a component
  // that closed after the callbacks Pi fired would drop the steps still to
  // come. The key is "<module>/<export>/<case name>" and the case's wire
  // node is its vector's `root`.
  const kitWidgetCases = {};
  {
    const branch = (expanded, outputPad = 1) => () => {
      const node = new piCodingAgent.BranchSummaryMessageComponent({ summary: "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`." }, undefined, outputPad);
      if (expanded) node.setExpanded(true);
      const root = new Container();
      root.addChild(node);
      return { root };
    };
    const prefix = "pi-coding-agent/BranchSummaryMessageComponent/";
    kitWidgetCases[`${prefix}collapsed`] = branch(false);
    kitWidgetCases[`${prefix}click toggles`] = branch(false);
    kitWidgetCases[`${prefix}right click ignored`] = branch(false);
    kitWidgetCases[`${prefix}expanded`] = branch(true);
    kitWidgetCases[`${prefix}outputPad 0 collapsed`] = branch(false, 0);
    kitWidgetCases[`${prefix}outputPad 0 expanded`] = branch(true, 0);
    kitWidgetCases[`${prefix}empty summary expanded`] = () => {
      const node = new piCodingAgent.BranchSummaryMessageComponent({ summary: "" }, undefined, 1);
      node.setExpanded(true);
      return { root: node };
    };
  }
  {
    const compaction = (tokens, expanded, outputPad = 1) => () => {
      const node = new piCodingAgent.CompactionSummaryMessageComponent({ summary: "Compacted the first **forty** turns:\n\n1. the build setup\n2. the failing test\n\nThe fix is pending.", tokensBefore: tokens }, undefined, outputPad);
      if (expanded) node.setExpanded(true);
      const root = new Container();
      root.addChild(node);
      return { root };
    };
    const prefix = "pi-coding-agent/CompactionSummaryMessageComponent/";
    kitWidgetCases[`${prefix}collapsed`] = compaction(123456, false);
    kitWidgetCases[`${prefix}click toggles`] = compaction(123456, false);
    kitWidgetCases[`${prefix}right click ignored`] = compaction(123456, false);
    kitWidgetCases[`${prefix}expanded`] = compaction(123456, true);
    kitWidgetCases[`${prefix}outputPad 0 collapsed`] = compaction(123456, false, 0);
    kitWidgetCases[`${prefix}outputPad 0 expanded`] = compaction(123456, true, 0);
    kitWidgetCases[`${prefix}tokens 999`] = compaction(999, false);
    kitWidgetCases[`${prefix}tokens 1234567 expanded`] = compaction(1234567, true);
    kitWidgetCases[`${prefix}empty summary tokens 0 expanded`] = () => {
      const node = new piCodingAgent.CompactionSummaryMessageComponent({ summary: "", tokensBefore: 0 }, undefined, 1);
      node.setExpanded(true);
      return { root: node };
    };
  }
  {
    const skill = (expanded, outputPad = 1) => () => {
      const node = new piCodingAgent.SkillInvocationMessageComponent({ name: "code-review", content: "Review the diff for **bugs** first.\n\n- correctness\n- naming\n\nReport findings as a list." }, undefined, outputPad);
      if (expanded) node.setExpanded(true);
      const root = new Container();
      root.addChild(node);
      return { root };
    };
    const prefix = "pi-coding-agent/SkillInvocationMessageComponent/";
    kitWidgetCases[`${prefix}collapsed`] = skill(false);
    kitWidgetCases[`${prefix}click toggles`] = skill(false);
    kitWidgetCases[`${prefix}right click ignored`] = skill(false);
    kitWidgetCases[`${prefix}expanded`] = skill(true);
    kitWidgetCases[`${prefix}outputPad 0 collapsed`] = skill(false, 0);
    kitWidgetCases[`${prefix}outputPad 0 expanded`] = skill(true, 0);
    kitWidgetCases[`${prefix}empty skill expanded`] = () => {
      const node = new piCodingAgent.SkillInvocationMessageComponent({ name: "", content: "" }, undefined, 1);
      node.setExpanded(true);
      return { root: node };
    };
  }
  {
    const custom = (text, expanded, outputPad = 1) => () => {
      const node = new piCodingAgent.CustomMessageComponent({ customType: "ci-status", content: text }, undefined, undefined, outputPad);
      if (expanded) node.setExpanded(true);
      const root = new Container();
      root.addChild(node);
      return { root };
    };
    const body = "Build **passed** in 12s\n\n- 214 tests\n- 0 failures";
    const prefix = "pi-coding-agent/CustomMessageComponent/";
    kitWidgetCases[`${prefix}default`] = custom(body, false);
    kitWidgetCases[`${prefix}outputPad 0`] = custom(body, false, 0);
    kitWidgetCases[`${prefix}expanded changes nothing`] = custom(body, true);
    kitWidgetCases[`${prefix}empty text`] = custom("", false);
    kitWidgetCases[`${prefix}no id`] = () => ({ root: new piCodingAgent.CustomMessageComponent({ customType: "note", content: "plain" }, undefined, undefined, 1) });
  }
  {
    const cancellable = (message, { spinner, messageColor, indicator, frame } = {}) => ({ theme, note }) => {
      const style = (token) => (token ? (text) => theme.fg(token, text) : (text) => text);
      const loader = new piTui.CancellableLoader({ requestRender() {} }, style(spinner), style(messageColor), message, indicator);
      loader.stop();
      if (frame !== undefined) { loader.currentFrame = frame % loader.frames.length; loader.updateDisplay(); }
      loader.onAbort = () => note("abort:");
      const root = new Container();
      root.addChild(loader);
      return { root, focus: loader };
    };
    const bordered = (message, options) => ({ theme, note }) => {
      const loader = new piCodingAgent.BorderedLoader({ requestRender() {} }, theme, message, options);
      loader.loader.stop();
      loader.onAbort = () => note("abort:");
      const root = new Container();
      root.addChild(loader);
      return { root, focus: loader };
    };
    const framed = (build, frame) => (ctx) => {
      const result = build(ctx);
      const inner = result.focus.loader;
      inner.currentFrame = frame % inner.frames.length;
      inner.updateDisplay();
      return result;
    };
    const wide = "A very long message that wraps around the narrow widths of the table";
    const c = "pi-tui/CancellableLoader/";
    for (const name of ["default", "escape aborts", "escape twice aborts twice", "other keys are ignored"]) kitWidgetCases[c + name] = cancellable("Working");
    kitWidgetCases[`${c}default message`] = cancellable("Loading...");
    kitWidgetCases[`${c}colors`] = cancellable("Working", { spinner: "accent", messageColor: "muted" });
    kitWidgetCases[`${c}frame 3`] = cancellable("Working", { frame: 3 });
    kitWidgetCases[`${c}indicator frames`] = cancellable("Working", { indicator: { frames: ["*", "+", "x"], intervalMs: 50 }, frame: 1 });
    kitWidgetCases[`${c}hidden indicator`] = cancellable("Working", { indicator: { frames: [] } });
    kitWidgetCases[`${c}wide message`] = cancellable(wide, { spinner: "accent" });
    const b = "pi-coding-agent/BorderedLoader/";
    for (const name of ["cancellable", "escape aborts", "escape twice aborts twice", "other keys are ignored"]) kitWidgetCases[b + name] = bordered("Fetching models");
    for (const name of ["not cancellable", "escape ignored when not cancellable"]) kitWidgetCases[b + name] = bordered("Fetching models", { cancellable: false });
    kitWidgetCases[`${b}default message`] = bordered("Loading...");
    kitWidgetCases[`${b}frame 4`] = framed(bordered("Fetching models"), 4);
    kitWidgetCases[`${b}wide message`] = bordered("Fetching the models of every configured provider, one after another");
  }
  {
    const input = ({ value, focused = true, options = {}, token } = {}) => ({ theme, note }) => {
      const component = new piTui.Input(token ? { ...options, placeholderStyle: (text) => theme.fg(token, text) } : options);
      if (value) component.setValue(value);
      component.focused = focused;
      component.onSubmit = (text) => note(`submit:${text}`);
      component.onEscape = () => note(`escape:${component.getValue()}`);
      const root = new Container();
      root.addChild(component);
      return { root, focus: component };
    };
    const i = "pi-tui/Input/";
    kitWidgetCases[`${i}empty`] = input({ focused: false });
    for (const name of ["empty focused", "typing", "left then insert", "backspace and delete", "home and end", "kill word and yank", "kill to end", "paste scrolls", "submit"]) kitWidgetCases[i + name] = input();
    kitWidgetCases[`${i}placeholder`] = input({ focused: false, options: { placeholder: "type here" } });
    kitWidgetCases[`${i}placeholder focused colored`] = input({ options: { placeholder: "type here" }, token: "dim" });
    kitWidgetCases[`${i}custom prompt`] = input({ options: { prompt: "$ " }, value: "ls -la" });
    kitWidgetCases[`${i}value`] = input({ focused: false, value: "hello world" });
    for (const name of ["value focused", "click moves the cursor", "right click ignored"]) kitWidgetCases[i + name] = input({ value: "hello world" });
    kitWidgetCases[`${i}wide characters`] = input({ value: "漢字 and 😀 text" });
    kitWidgetCases[`${i}submit newline key`] = input({ value: "preset" });
    kitWidgetCases[`${i}escape`] = input({ value: "keep" });
  }
  {
    const commands = [
      { name: "help", description: "Show help" },
      { name: "model", description: "Switch the model", argumentHint: "<name>" },
      { name: "quit", description: "Exit" },
      { name: "session", description: "Session info" },
      { name: "settings", description: "Open settings" },
      { name: "share" },
      { name: "skills", description: "List skills" },
      { name: "compact", description: "Compact the context" },
    ];
    const editor = ({ value, padding, acmax, border, disable, history = [], auto = false, focused = true } = {}) => ({ tui, theme, note }) => {
      const options = {};
      if (padding !== undefined) options.paddingX = padding;
      if (acmax !== undefined) options.autocompleteMaxVisible = acmax;
      const component = new piTui.Editor(tui, { borderColor: (text) => theme.fg("borderMuted", text), selectList: piCodingAgent.getSelectListTheme() }, options);
      if (border) component.borderColor = (text) => theme.fg(border, text);
      if (disable) component.disableSubmit = true;
      for (const entry of history) component.addToHistory(entry);
      if (auto) component.setAutocompleteProvider(new piTui.CombinedAutocompleteProvider(commands, process.cwd()));
      if (value) component.setText(value);
      component.focused = focused;
      component.onSubmit = (text) => note(`submit:${text}`);
      const root = new Container();
      root.addChild(component);
      return { root, focus: component };
    };
    const e = "pi-tui/Editor/";
    kitWidgetCases[e + "empty"] = editor({ focused: false });
    kitWidgetCases[e + "empty focused"] = editor();
    kitWidgetCases[e + "value"] = editor({ value: "hello world", focused: false });
    kitWidgetCases[e + "multiline value focused"] = editor({ value: "one\ntwo\nthree" });
    kitWidgetCases[e + "wrapped line"] = editor({ value: "The quick brown fox jumps over the lazy dog and keeps running through the field" });
    kitWidgetCases[e + "wide characters"] = editor({ value: "漢字 and 😀 text" });
    kitWidgetCases[e + "typing"] = editor();
    kitWidgetCases[e + "newline"] = editor();
    kitWidgetCases[e + "backspace and delete"] = editor();
    kitWidgetCases[e + "kill word and yank"] = editor();
    kitWidgetCases[e + "undo"] = editor();
    kitWidgetCases[e + "submit"] = editor();
    kitWidgetCases[e + "paste"] = editor();
    kitWidgetCases[e + "paste large"] = editor();
    kitWidgetCases[e + "cursor movement"] = editor({ value: "one\ntwo\nthree" });
    kitWidgetCases[e + "submit multiline"] = editor({ value: "one\ntwo" });
    kitWidgetCases[e + "submit disabled"] = editor({ value: "keep", disable: true });
    kitWidgetCases[e + "padding"] = editor({ value: "padded text", padding: 2 });
    kitWidgetCases[e + "padding wrapped"] = editor({ value: "The quick brown fox jumps over the lazy dog and keeps running", padding: 3 });
    kitWidgetCases[e + "border color"] = editor({ value: "colored", border: "accent" });
    kitWidgetCases[e + "history"] = editor({ history: ["first", "second", "third"] });
    kitWidgetCases[e + "history then edit"] = editor({ history: ["first", "second"] });
    kitWidgetCases[e + "scrolls past the visible lines"] = editor({ value: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12" });
    kitWidgetCases[e + "scroll indicator below"] = editor({ value: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12" });
    kitWidgetCases[e + "more rows show more lines"] = editor({ value: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12" });
    kitWidgetCases[e + "few rows show five lines"] = editor({ value: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12" });
    kitWidgetCases[e + "page up and down"] = editor({ value: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12\nline 13\nline 14\nline 15\nline 16\nline 17\nline 18\nline 19\nline 20\nline 21\nline 22\nline 23\nline 24\nline 25\nline 26\nline 27\nline 28\nline 29\nline 30" });
    kitWidgetCases[e + "click moves the cursor"] = editor({ value: "hello\nworld" });
    kitWidgetCases[e + "click on the border"] = editor({ value: "hello\nworld" });
    kitWidgetCases[e + "right click ignored"] = editor({ value: "hello" });
    kitWidgetCases[e + "slash opens the menu"] = editor({ auto: true });
    kitWidgetCases[e + "slash narrows"] = editor({ auto: true });
    kitWidgetCases[e + "slash no match"] = editor({ auto: true });
    kitWidgetCases[e + "slash tab accepts"] = editor({ auto: true });
    kitWidgetCases[e + "slash arrow then tab"] = editor({ auto: true });
    kitWidgetCases[e + "slash enter accepts"] = editor({ auto: true });
    kitWidgetCases[e + "slash escape closes"] = editor({ auto: true });
    kitWidgetCases[e + "slash click selects"] = editor({ auto: true });
    kitWidgetCases[e + "text after the slash opens nothing"] = editor({ auto: true });
    kitWidgetCases[e + "slash max visible 3"] = editor({ acmax: 3, auto: true });
    kitWidgetCases[e + "slash with padding"] = editor({ padding: 2, auto: true });
    kitWidgetCases[e + "autocomplete menu does not follow disabled submit"] = editor({ disable: true, auto: true });
  }
  // The child the scroll-view and mouse-region cases wrap: a text of wrapping rows, a markdown, an empty container, or a
  // branch summary (which a click toggles).
  const kitScrollLines = "Alpha line of the log\nBeta line of the log\nGamma line of the log that is long enough to wrap at the narrow widths\nDelta";
  const kitScrollMarkdown = "# Title\n\n- one\n- two with a long line that wraps at narrow widths\n\n`code`";
  const kitScrollChild = (kind) => {
    if (kind === "md") return new piTui.Markdown(kitScrollMarkdown, 1, 0, piCodingAgent.getMarkdownTheme());
    if (kind === "region") return new piTui.Text("Region content\nsecond row", 1, 0);
    if (kind === "empty") return new Container();
    if (kind === "branch") return new piCodingAgent.BranchSummaryMessageComponent({ summary: "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`." }, undefined, 1);
    return new piTui.Text(kitScrollLines, 1, 0);
  };
  {
    const styled = (theme, token) => (token ? (text) => theme.fg(token, text) : undefined);
    const scroll = (o, kind = "text", wrap) => ({ theme }) => {
      const options = {};
      if (o.follow) options.follow = o.follow;
      if (o.primary) options.primary = true;
      if (o.overscroll) options.overscroll = o.overscroll;
      if (o.scrollbar) options.scrollbar = o.scrollbar;
      if (o.track) options.scrollbarTrackStyle = styled(theme, o.track);
      if (o.thumb) options.scrollbarThumbStyle = styled(theme, o.thumb);
      if (o.delay !== undefined) options.scrollbarHideDelayMs = o.delay;
      const view = new piTui.ScrollView(kitScrollChild(kind), options);
      if (o.scrollTop !== undefined) view.scrollTo(o.scrollTop);
      const root = new Container();
      if (wrap === "hf") root.addChild(new piTui.Text("Header", 1, 0));
      root.addChild(view);
      if (wrap === "hf") root.addChild(new piTui.Text("Footer", 1, 0));
      if (wrap === "after") root.addChild(new piTui.Text("after", 1, 0));
      return { root };
    };
    const s = "pi-tui/ScrollView/";
    kitWidgetCases[s + "default"] = scroll({}, "text", undefined);
    kitWidgetCases[s + "scrollbar always"] = scroll({"scrollbar": "always"}, "text", undefined);
    kitWidgetCases[s + "scrollbar auto"] = scroll({"scrollbar": "auto"}, "text", undefined);
    kitWidgetCases[s + "scrollbar colors"] = scroll({"scrollbar": "always", "track": "dim", "thumb": "accent"}, "text", undefined);
    kitWidgetCases[s + "follow end"] = scroll({"follow": "end"}, "text", undefined);
    kitWidgetCases[s + "primary and contain"] = scroll({"overscroll": "contain", "primary": true}, "text", undefined);
    kitWidgetCases[s + "hide delay"] = scroll({"scrollbar": "auto", "delay": 50}, "text", undefined);
    kitWidgetCases[s + "scroll top clamps"] = scroll({"scrollTop": 3}, "text", undefined);
    kitWidgetCases[s + "markdown child"] = scroll({"scrollbar": "always"}, "md", undefined);
    kitWidgetCases[s + "after other rows"] = scroll({"scrollbar": "always"}, "text", "hf");
    kitWidgetCases[s + "empty child"] = scroll({"scrollbar": "always"}, "empty", undefined);
    kitWidgetCases[s + "wheel and click pass"] = scroll({}, "text", "after");
  }
  {
    const region = (o, kind = "text", wrap) => ({ note }) => {
      const mouseRegion = new piTui.MouseRegion(kitScrollChild(kind), (event) => {
        if (o.types && !o.types.includes(event.type)) return undefined;
        note(`mouse:${event.type}/${event.button}/${event.x}/${event.y}`);
        const result = { handled: o.handled ?? true };
        if (o.capture) result.capture = true;
        if (o.focus) result.focus = true;
        if (o.render !== undefined) result.render = o.render;
        return result;
      });
      const root = new Container();
      if (wrap === "ab") root.addChild(new piTui.Text("Above the region", 1, 0));
      root.addChild(mouseRegion);
      if (wrap === "ab") root.addChild(new piTui.Text("Below", 1, 0));
      return { root };
    };
    const m = "pi-tui/MouseRegion/";
    kitWidgetCases[m + "renders its child"] = region({}, "region", undefined);
    kitWidgetCases[m + "every type by default"] = region({}, "region", undefined);
    kitWidgetCases[m + "types filter"] = region({"types": ["click"]}, "region", undefined);
    kitWidgetCases[m + "not handled"] = region({"handled": false}, "region", undefined);
    kitWidgetCases[m + "capture"] = region({"capture": true}, "region", undefined);
    kitWidgetCases[m + "focus and render"] = region({"focus": true, "render": false}, "region", undefined);
    kitWidgetCases[m + "local coordinates"] = region({}, "region", "ab");
    kitWidgetCases[m + "the child takes the event first"] = region({}, "branch", undefined);
    kitWidgetCases[m + "right button"] = region({}, "region", undefined);
  }
  // kit-widget-cases
  pi.registerCommand("kit-widget", {
    description: "Show a widget kind's Pi vector case (D107)",
    handler: async (key, ctx) => {
      const build = kitWidgetCases[key];
      if (!build) throw new Error(`kit-widget: unknown case ${JSON.stringify(key)}`);
      await ctx.ui.custom((tui, theme, keys) => {
        const note = (entry) => ctx.ui.notify(`kit-widget-event=${entry}`, "info");
        // build returns the root component and, when a node of it holds the focus, that node.
        const { root, focus } = build({ tui, theme, keys, note });
        if (focus) root.handleInput = (data) => focus.handleInput(data);
        else if (!root.handleInput) root.handleInput = () => {};
        return root;
      }, { overlayOptions: { title: "Kit" } });
    },
  });
  pi.registerCommand("mouse-probe", {
    description: "Exercise extension mouse input",
    handler: async (_args, ctx) => {
      const result = await ctx.ui.custom((_tui, _theme, _keys, done) => new MouseProbe(done), { overlay: true });
      ctx.ui.notify(`mouse=${result ?? ""}`, "info");
    },
  });
  pi.registerMessageRenderer("kit-message", (_message, _options, theme) => new KitProbe(theme));
  pi.registerEntryRenderer("kit-entry", (_entry, _options, theme) => new KitProbe(theme));
  pi.registerCommand("kit-surfaces", {
    description: "Show the kit probe on every view surface (D107)",
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("kit-probe", (_tui, theme) => new KitProbe(theme));
      ctx.ui.setHeader((_tui, theme) => new KitProbe(theme));
      ctx.ui.setFooter((_tui, theme) => new KitProbe(theme));
      const kitTool = {
        name: "kit_view_tool",
        label: "kit_view_tool",
        description: "Render its result as the kit probe",
        parameters: { type: "object", properties: {} },
        async execute() { return { content: [{ type: "text", text: "kit" }] }; },
      };
      kitTool.renderCall = (_args, theme) => new KitProbe(theme);
      kitTool.renderResult = (_result, _options, theme) => new KitProbe(theme);
      pi.registerTool(kitTool);
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
        c.addChild(new piTui.Image(kitImagePNG(image), "image/png", { fallbackColor: (s) => s }));
        c.addChild(new piTui.Text(args, 0, 0));
        return c;
      });
    },
  });
  // headless-ui-probe hands the runtime footer, header, custom, editor, terminal-input and theme calls; its argument is a file every closure appends a marker to when it runs (TestConformance_HeadlessModesNeverRunUIClosures).
  pi.registerCommand("headless-ui-probe", {
    description: "Hand the runtime footer, header and custom component closures",
    handler: async (path, ctx) => {
      const mark = (line) => fs.appendFileSync(path, `${line}\n`);
      ctx.ui.setFooter(() => {
        mark("node:footer");
        return { render: () => { mark("node:footer-render"); return ["node:footer"]; }, invalidate() {} };
      });
      ctx.ui.setHeader(() => {
        mark("node:header");
        return { render: () => { mark("node:header-render"); return ["node:header"]; }, invalidate() {} };
      });
      await ctx.ui.custom((_tui, _theme, _keybindings, done) => {
        mark("node:custom");
        return { render: () => { mark("node:custom-render"); return ["node:custom"]; }, handleInput: () => done("closed"), invalidate() {} };
      });
      ctx.ui.setEditorComponent(() => { mark("node:editor"); return { render: () => ["node:editor"], handleInput() {}, invalidate() {} }; });
      mark(`node:getEditorComponent:${typeof ctx.ui.getEditorComponent()}`);
      const unsubscribe = ctx.ui.onTerminalInput(() => { mark("node:terminal-input"); });
      mark(`node:onTerminalInput:${typeof unsubscribe}`);
      unsubscribe();
      ctx.ui.setWorkingMessage("node:working");
      ctx.ui.setWorkingIndicator({ frames: ["node:working"] });
      try {
        mark(`node:setTheme:${JSON.stringify(ctx.ui.setTheme(ctx.ui.theme))}`);
      } catch (error) {
        mark(`node:setTheme:threw:${error?.message}`);
      }
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
        const box = new piTui.Box(1, 0, (s) => theme.bg("customMessageBg", s));
        box.addChild(new piTui.Text("boxed", 0, 0));
        // getSettingsListTheme() draws the cursor once, when it is called;
        // a getter draws it at render time, under the surface's viewTheme.
        const settingsTheme = Object.defineProperty({ ...getSettingsListTheme() }, "cursor", { get: () => getSettingsListTheme().cursor, enumerable: true });
        const settings = new piTui.SettingsList([
          { id: "theme", label: "Theme", description: "Color theme", currentValue: "dark", values: ["dark", "light"] },
          { id: "wrap", label: "Wrap", description: "Wrap lines", currentValue: "on", values: ["on", "off"] },
        ], 3, settingsTheme, () => {}, () => {});
        const loader = new piTui.Loader(tui, (s) => theme.fg("accent", s), (s) => theme.fg("muted", s), "Working", { frames: ["*"] });
        const tracks = {
          render: () => ["track one", "track two"],
          invalidate() {},
        };
        // The list rides on the component itself: a frontend draws it, a terminal draws the lines.
        tracks.viewLines = { list: { items: [{ label: "track one", detail: "A" }, { label: "track two", detail: "B" }], selectedIndex: 1 } };
        const stack = new piTui.VStack([box, settings, loader, new piTui.Image(kitKindsPNG, "image/png", { fallbackColor: (s) => s }), tracks], { gap: 1 });
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
          const tool = new piCodingAgent.ToolExecutionComponent(name, callId, toolArgs, {}, definition, tui, "/work/kit");
          tool.setArgsComplete();
          tool.markExecutionStarted();
          return tool;
        };
        if (!next || !kitKept) {
          const streaming = new piCodingAgent.AssistantMessageComponent();
          streaming.updateContent(reply(false), true);
          const ls = card("ls", "call-1", { path: "src" }, createLsToolDefinition("/work/kit"));
          ls.updateResult({ content: [{ type: "text", text: "a.go\nb.go" }] }, false);
          const grep = card("grep", "call-2", { pattern: "TODO" }, createGrepToolDefinition("/work/kit"));
          grep.updateResult({ content: [{ type: "text", text: "x.go:1: TODO kit" }] }, true);
          const exited = new piCodingAgent.BashExecutionComponent("ls -la", tui, false);
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
        const user = new piCodingAgent.UserMessageComponent("Fix **the** kit build\n\n- one\n- two");
        const failed = new piCodingAgent.AssistantMessageComponent({ content: [{ type: "thinking", thinking: "secret" }, { type: "text", text: "Visible kit" }], stopReason: "error", errorMessage: "kit-boom-7" });
        failed.setHideThinkingBlock(true);
        failed.setHiddenThinkingLabel("Pondering kit...");
        failed.setOutputPad(0);
        const read = card("read", "call-3", { path: "missing.txt" }, createReadToolDefinition("/work/kit"));
        read.updateResult({ content: [{ type: "text", text: "ENOENT: kit" }], isError: true }, false);
        read.setExpanded(true);
        const custom = card("kit_tool", "call-4", { q: "x" }, {});
        custom.updateResult({ content: [{ type: "text", text: "answer 42" }] }, false);
        const seq = new piCodingAgent.BashExecutionComponent("seq 25", tui, true);
        seq.appendOutput(Array.from({ length: 25 }, (_, i) => String(i + 1)).join("\n"));
        seq.setComplete(0, false);
        const diff = new piTui.Text(renderDiff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail", { filePath: "kit.go" }), 0, 0);
        const root = new Container();
        for (const child of [user, streaming, failed, ls, grep, read, custom, exited, seq, diff]) root.addChild(child);
        return root;
      });
    },
  });

  // kit-output-pad sets the "kit-output-pad" widget to a bash tool card and
  // a bash command with Pi 1.1.0's outputPad 3 and a final result's
  // durationMs 1234 (F7); "next" calls setOutputPad(2) on the kept
  // components and gives the result durationMs 61500.
  let padKept;
  pi.registerCommand("kit-output-pad", {
    description: "Show Pi's outputPad and durationMs (D107)",
    handler: async (args, ctx) => {
      const next = args === "next";
      ctx.ui.setWidget("kit-output-pad", (tui) => {
        const result = (durationMs) => ({ content: [{ type: "text", text: "kit out" }], durationMs });
        if (!next || !padKept) {
          const tool = new piCodingAgent.ToolExecutionComponent("bash", "call-p1", { command: "echo kit" }, { outputPad: 3 }, createBashToolDefinition("/work/kit"), tui, "/work/kit");
          tool.setArgsComplete();
          tool.markExecutionStarted();
          tool.updateResult(result(1234), false);
          const command = new piCodingAgent.BashExecutionComponent("echo pad", tui, false, 3);
          command.appendOutput("padded");
          command.setComplete(0, false);
          padKept = { tool, command };
        }
        const { tool, command } = padKept;
        if (next) {
          tool.setOutputPad(2);
          tool.updateResult(result(61500), false);
          command.setOutputPad(2);
        }
        const root = new Container();
        root.addChild(tool);
        root.addChild(command);
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
    if (event.toolName === "order_probe") { ctx.ui.notify(`order-result=${event.toolCallId}`, "info"); return; }
    const held = [["bash", isBashToolResult], ["powershell", isPowerShellToolResult], ["read", isReadToolResult], ["edit", isEditToolResult], ["write", isWriteToolResult], ["grep", isGrepToolResult], ["find", isFindToolResult], ["ls", isLsToolResult]].filter(([, is]) => is(event)).map(([name]) => name);
    ctx.ui.notify(`tool-result-guards=${held.join(",")}`, "info");
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
  // state-push-probe (TestHostStateChangeReachesABackgroundReaderAcrossSDKs) arms a timer that reads one replicated accessor from outside every request and returns: the host then changes its own state with no call and no event to this extension, and the timer reports the first value that differs from the one it armed on, or the value it still holds after the window, to a file in the directory the arguments name. The arguments are {"kind": "model" | "session-name" | "thinking-level" | "theme" | "settings:<key>" | "mcp", "dir": <directory>}; the report is "<armed>=><read>".
  pi.registerCommand("state-push-probe", { description: "Read one replicated accessor from a timer between requests", handler: async (args, ctx) => {
    const { kind, dir } = JSON.parse(args);
    let read;
    if (kind === "model") read = () => ctx.model?.id ?? "";
    else if (kind === "session-name") read = () => pi.getSessionName() ?? "";
    else if (kind === "theme") read = () => ctx.ui.theme.name ?? "";
    else if (kind === "thinking-level") read = () => pi.getThinkingLevel();
    else if (kind.startsWith("settings:")) read = () => JSON.stringify(pi.getSettings()[kind.slice("settings:".length)] ?? null);
    else if (kind === "mcp") read = () => pi.getMcpServers().map((server) => server.name).join(",");
    else throw new Error(`state-push-probe: unsupported kind ${JSON.stringify(kind)}`);
    const guarded = () => { try { return read(); } catch (error) { return `error:${error.message}`; } };
    const armed = guarded();
    const deadline = Date.now() + 5000;
    const timer = setInterval(() => {
      const current = guarded();
      if (current === armed && Date.now() < deadline) return;
      clearInterval(timer);
      const report = joinPath(dir, `state-push-${kind.replaceAll(":", "_")}`);
      writeFileSync(`${report}.tmp`, `${armed}=>${current}`);
      renameSync(`${report}.tmp`, report);
    }, 2);
  } });
  registerSignalAbort(pi);
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

// The signal-abort row every SDK fixture carries. Pi's ctx.abort() aborts the run's signal before it returns (agent.ts:340-342), so a handler that holds the signal and reads it again right after the abort sees it aborted, in the same turn.
function registerSignalAbort(pi) {
  const state = (signal) => (signal === undefined ? "none" : signal.aborted ? "aborted" : "live");
  pi.registerCommand("signal-abort", {
    description: "Abort the run and report ctx.signal",
    handler: (_args, ctx) => {
      const held = ctx.signal;
      ctx.abort();
      ctx.ui.notify(`abort:${state(held)}:${state(ctx.signal)}`, "info");
    },
  });
}
