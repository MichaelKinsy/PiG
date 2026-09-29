// Pi loader.ts:238-243 guards every ExtensionAPI method before validation or effects.
export async function probeFailedAPI(api, onLateEvent = () => {}) {
  const calls = [
    ["on", () => api.on("factory-failure", () => {})],
    ["registerTool", () => api.registerTool({ name: "late-tool", parameters: {} })],
    ["registerCommand", () => api.registerCommand("late-command", { handler: async () => {} })],
    ["registerShortcut", () => api.registerShortcut("ctrl+shift+y", { handler: async () => {} })],
    ["registerFlag", () => api.registerFlag("late-flag", { type: "boolean", default: true })],
    ["registerMessageRenderer", () => api.registerMessageRenderer("late-message", () => {})],
    ["registerMarkdownTransformer", () => api.registerMarkdownTransformer(text => text)],
    ["registerEntryRenderer", () => api.registerEntryRenderer("late-entry", () => {})],
    ["getFlag", () => api.getFlag("failed-flag")],
    ["sendMessage", () => api.sendMessage({ customType: "late", content: "late", display: true })],
    ["sendUserMessage", () => api.sendUserMessage("late")],
    ["appendEntry", () => api.appendEntry("late")],
    ["setSessionName", () => api.setSessionName("late")],
    ["getSessionName", () => api.getSessionName()],
    ["setLabel", () => api.setLabel("late", "late")],
    ["exec", () => api.exec(process.execPath, ["-e", "process.exit(0)"])],
    ["getActiveTools", () => api.getActiveTools()],
    ["getAllTools", () => api.getAllTools()],
    ["setActiveTools", () => api.setActiveTools([])],
    ["getCommands", () => api.getCommands()],
    ["setModel", () => api.setModel({ provider: "late", id: "late" })],
    ["getThinkingLevel", () => api.getThinkingLevel()],
    ["setThinkingLevel", () => api.setThinkingLevel("high")],
    ["registerProvider", () => api.registerProvider("late-provider", { baseUrl: "https://provider.test/v1", apiKey: "provider-test-key" })],
    ["unregisterProvider", () => api.unregisterProvider("working-provider")],
    ["events.emit", () => api.events.emit("factory-failure", undefined)],
    ["events.on", () => api.events.on("factory-failure", onLateEvent)],
  ];
  const results = [];
  for (const [method, call] of calls) {
    let asynchronous = false;
    let error = null;
    try {
      const result = call();
      if (result && typeof result.then === "function") {
        asynchronous = true;
        await result;
      }
    } catch (failure) {
      error = failure.message;
    }
    results.push({ method, asynchronous, error });
  }
  return results;
}
