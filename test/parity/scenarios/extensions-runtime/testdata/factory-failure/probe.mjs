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
    ["registerToolRenderer", () => api.registerToolRenderer(() => undefined)],
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
    // loader.ts:411-414, 456-497: the 0.99.1 methods reject after a failed factory like every other captured call.
    ["getSettings", () => api.getSettings()],
    ["registerMcpServer", () => api.registerMcpServer("late-server", { url: "https://mcp.test" })],
    ["unregisterMcpServer", () => api.unregisterMcpServer("late-server")],
    ["getMcpServers", () => api.getMcpServers()],
    ["registerVirtualModel", () => api.registerVirtualModel({ provider: "late", id: "late", name: "Late", route: () => ({}) })],
    ["unregisterVirtualModel", () => api.unregisterVirtualModel("late", "late")],
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
