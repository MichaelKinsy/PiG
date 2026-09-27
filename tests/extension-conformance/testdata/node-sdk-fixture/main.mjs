export default function (pi) {
  const loginDefinition = () => ({
    brand: Array.from({ length: 5 }, () => "A".repeat(41)),
    hero: Array.from({ length: 14 }, () => "A".repeat(32)),
    mascot: Array.from({ length: 14 }, () => "A".repeat(16)),
    palette: { A: "#123ABC" },
    name: "Conformance Pig",
    description: "Cross-language login fixture",
    tagline: "One canonical definition across every SDK",
  });

  pi.registerMessageRenderer("conformance-message", (message, options) => ({
    render(width) { return message.content === "padding-options" ? [JSON.stringify(options)] : [`renderer:${message.content}:expanded=${Boolean(options.expanded)}:width=${width}`]; },
    invalidate() {},
  }));
  pi.registerEntryRenderer("conformance-entry", (entry, options) => ({
    render(width) { return [`entryrenderer:${entry.data}:expanded=${Boolean(options.expanded)}:width=${width}`]; },
    invalidate() {},
  }));

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
      return { render: (width) => [`toolrender:result:${result.content[0].text}:${result.details.k}:expanded=${Boolean(options.expanded)}:calls=${calls}:width=${width}`], invalidate() {} };
    },
  });
  pi.registerTool({
    name: "echo",
    description: "Echo input text",
    parameters: { type: "object", required: ["text"], properties: { text: { type: "string", description: "Text to echo" } } },
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
        maxTokens: 321, temperature: 0.65, samplingParams: { topP: 0.8 },
        thinkingBudgets: { minimal: 11, low: 22, medium: 33, high: 44 }, thinking: "high", isReasoning: true,
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
  pi.registerCommand("report_geometry", { description: "Report observed terminal geometry", handler: async (_args, ctx) => ctx.ui.notify(`geometry:${ctx.width}x${ctx.height}`, "info") });

  let unsubscribeTerminalInput;
  pi.registerCommand("term_subscribe", {
    description: "Subscribe to raw terminal input",
    handler: async (_args, ctx) => {
      unsubscribeTerminalInput = ctx.ui.onTerminalInput((data) => {
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
  pi.registerCommand("append_entry", { description: "Append a custom entry", handler: async () => pi.appendEntry("conformance-entry", "hello-entry") });
  pi.registerCommand("context-probe", {
    description: "Report ctx.mode + ctx.getSystemPromptOptions()",
    handler: async (_args, ctx) => {
      const options = ctx.getSystemPromptOptions();
      ctx.ui.notify(`mode=${ctx.mode} trusted=${ctx.isProjectTrusted()} spo_prompt=${options.customPrompt ?? ""} spo_cwd=${options.cwd ?? ""} spo_tools=${(options.selectedTools ?? []).join(",")}`, "info");
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
  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "powershell" && event.toolName !== "bash") return;
    ctx.ui.notify(`tool-call=${event.toolName}:${event.input?.command}:${event.input?.timeout}`, "info");
    if (event.input?.command === "blocked-command") return { block: true, reason: `blocked ${event.toolName}` };
  });
  pi.on("tool_result", async (event, ctx) => {
    if (event.toolName !== "powershell" && event.toolName !== "bash") return;
    ctx.ui.notify(`tool-result=${event.toolName}:${event.details?.fullOutputPath}:${event.details?.truncation?.totalLines}:${event.content?.[0]?.text}`, "info");
    return { content: [{ type: "text", text: `${event.toolName} redacted` }] };
  });
  pi.on("message_update", async (event, ctx) => {
    const assistant = event.assistantMessageEvent ?? {};
    ctx.ui.notify(`message-update=${assistant.type}:${assistant.contentIndex}:${assistant.delta}:${Object.hasOwn(assistant, "assistantMessageEvent")}`, "info");
  });
  pi.on("tool_execution_update", async (event, ctx) => {
    if (event.toolName === "production_tool") {
      ctx.ui.notify(`tool-update=${event.toolName}:${event.args?.path}:${event.args?.nested?.depth}:${event.partialResult?.content}:${event.partialResult?.details?.progress}`, "info");
      return;
    }
    ctx.ui.notify(`tool-update=${event.toolName}:${JSON.stringify(event.args)}:${event.partialResult?.content}:${event.partialResult?.details?.progress}`, "info");
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
  pi.on("project_trust", async () => ({ trusted: "yes", remember: true }));
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
  pi.on("session_start", async (event, ctx) => ctx.ui.notify(`session_start:${event.reason ?? ""}`, "info"));
  pi.on("session_shutdown", async (event, ctx) => ctx.ui.notify(`session_shutdown:${event.reason ?? ""}`, "info"));
  pi.on("session_info_changed", async (event, ctx) => ctx.ui.notify(`session_info_changed:${event.name ?? ""}`, "info"));
  pi.on("session_before_compact", async (event, ctx) => ctx.ui.notify(`session_before_compact:${event.reason ?? ""}:${Boolean(event.willRetry)}`, "info"));
  pi.on("session_compact", async (event, ctx) => ctx.ui.notify(`session_compact:${event.reason ?? ""}:${Boolean(event.willRetry)}:${Boolean(event.fromExtension)}`, "info"));
  pi.on("session_compact_failed", async (event, ctx) => ctx.ui.notify(`session_compact_failed:${event.reason ?? ""}:${event.errorMessage ?? ""}:${Boolean(event.aborted)}:${Boolean(event.willRetry)}:${Boolean(event.fromExtension)}`, "info"));
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
