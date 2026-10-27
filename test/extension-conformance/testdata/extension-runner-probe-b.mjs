// Probe extension B of the extension_runner row (extension_runner_test.go): it loads after extension A, so its handlers see what A's handlers left, and it registers a tool, a command and a shortcut under names A used.
export default function (pi) {
  const schema = { type: "object", properties: {}, additionalProperties: false };
  const execute = async () => ({ content: [{ type: "text", text: "ok" }], details: {} });
  pi.registerFlag("b-flag", { description: "A boolean flag", type: "boolean", default: true });
  pi.registerTool({ name: "beta", label: "Beta", description: "Beta tool", parameters: schema, execute });
  pi.registerTool({ name: "shared", label: "Shared B", description: "Shared tool from B", parameters: schema, execute });
  pi.registerCommand("twin", { description: "Twin from B", handler: async () => {} });
  pi.registerShortcut("ctrl+shift+p", { description: "Probe shortcut B", handler: async () => {} });
  pi.registerEntryRenderer("probe-entry", () => undefined);
  pi.registerMarkdownTransformer((markdown) => markdown);

  const note = (kind, data) => pi.appendEntry("b_" + kind, data);
  pi.on("session_start", async (event) => note("session_start", { reason: event.reason }));
  pi.on("agent_start", async () => note("agent_start", {}));
  pi.on("session_before_switch", async () => ({ cancel: true }));
  pi.on("tool_call", async (event) => {
    note("tool_call", { command: event.input.command ?? null });
    if (String(event.input.command ?? "").includes("forbidden")) return { block: true, reason: "blocked by b" };
  });
  pi.on("tool_result", async (event) => {
    note("tool_result", { text: event.content[0].text });
    return { details: { b: event.content[0].text } };
  });
  pi.on("input", async (event) => {
    note("input", { text: event.text });
    if (event.text === "handled-me") return { action: "handled" };
  });
  pi.on("context", async (event) => {
    note("context", { count: event.messages.length });
    return { messages: event.messages.filter((message) => message.content !== "drop-me") };
  });
  pi.on("before_agent_start", async (event) => ({ message: { customType: "probe", content: "b-saw:" + event.systemPrompt.endsWith("[A]"), display: true } }));
  pi.on("before_provider_request", async (event) => ({ ...event.payload, b: 2 }));
  pi.on("resources_discover", async () => ({ promptPaths: ["/prompts/b"] }));
  pi.on("turn_end", async (event) => note("turn_end", { entries: event.entries.length, continue: event.continue }));
}
