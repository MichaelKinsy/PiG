// Probe extension A of the extension_runner row (extension_runner_test.go): it registers one of each kind of thing the ExtensionRunner reads, and a handler for each event the runner emits. A handler records what it saw through pi.appendEntry, which the script's bound appendEntry action logs, so the report shows both the dispatch and the actions an extension's calls reach.
export default function (pi) {
  const schema = { type: "object", properties: { text: { type: "string" } }, additionalProperties: false };
  const execute = async () => ({ content: [{ type: "text", text: "ok" }], details: {} });
  pi.registerFlag("probe-flag", { description: "A string flag", type: "string", default: "dflt" });
  pi.registerFlag("probe-bool", { description: "A flag without a default", type: "boolean" });
  pi.registerMcpServer("probe-mcp", { command: "probe-mcp-server" });
  pi.registerTool({ name: "alpha", label: "Alpha", description: "Alpha tool", promptSnippet: "alpha snippet", parameters: schema, execute });
  pi.registerTool({ name: "shared", label: "Shared", description: "Shared tool from A", parameters: schema, execute });
  pi.registerCommand("hello", { description: "Hello from A", handler: async () => {} });
  pi.registerCommand("twin", { description: "Twin from A", handler: async () => {} });
  pi.registerShortcut("ctrl+shift+p", { description: "Probe shortcut A", handler: async () => {} });
  pi.registerShortcut("ctrl+c", { description: "Reserved by the host", handler: async () => {} });
  pi.registerShortcut("ctrl+k", { description: "Soft conflict", handler: async () => {} });
  pi.registerMessageRenderer("probe-message", () => undefined);
  pi.registerToolRenderer((toolName, next) => (toolName === "alpha" ? { renderShell: "self", renderCall: () => undefined } : next()));

  const note = (kind, data) => pi.appendEntry("a_" + kind, data);
  pi.on("session_start", async (event, ctx) => {
    note("session_start", {
      reason: event.reason, cwd: ctx.cwd, idle: ctx.isIdle(), hasUI: ctx.hasUI, prompt: ctx.getSystemPrompt(), model: ctx.model ? ctx.model.id : null,
      active: pi.getActiveTools(), name: pi.getSessionName(), tools: pi.getAllTools().map((tool) => tool.name), thinking: pi.getThinkingLevel(), trusted: ctx.isProjectTrusted(),
    });
  });
  pi.on("agent_start", async () => {
    throw new Error("a failed");
  });
  pi.on("session_before_switch", async () => ({ cancel: false }));
  pi.on("tool_call", async (event) => {
    if (event.toolName === "bash") event.input.command += " #a";
  });
  pi.on("tool_result", async (event) => ({ content: [{ type: "text", text: "A(" + event.content[0].text + ")" }] }));
  pi.on("input", async (event) => (event.text.startsWith("t:") ? { action: "transform", text: event.text + "!" } : undefined));
  pi.on("context", async (event) => ({ messages: [...event.messages, { role: "user", content: "from a", timestamp: 3 }] }));
  pi.on("before_agent_start", async (event) => ({
    message: { customType: "probe", content: "a:" + event.prompt, display: false },
    systemPrompt: event.systemPrompt + "\n[A]",
  }));
  pi.on("message_end", async (event) => ({ message: { ...event.message, content: "A-end" } }));
  pi.on("before_provider_request", async (event) => ({ ...event.payload, a: 1 }));
  pi.on("before_provider_headers", async (event) => {
    event.headers["x-a"] = "1";
    delete event.headers["x-del"];
  });
  pi.on("user_bash", async () => ({ result: { output: "intercepted", exitCode: 0, cancelled: false, truncated: false } }));
  pi.on("resources_discover", async () => ({ skillPaths: ["/skills/a"] }));
  pi.on("cache_warming_decision", async () => ({ action: "stop" }));
  pi.on("ui_prompt_start", async (event) => note("ui", { type: event.type, kind: event.kind, title: event.title ?? null }));
  pi.on("ui_prompt_end", async (event) => note("ui", { type: event.type, kind: event.kind, title: event.title ?? null }));
  pi.on("turn_end", async () => ({ entries: [{ type: "custom", customType: "drafted", data: { n: 1 } }], continue: true }));
}
