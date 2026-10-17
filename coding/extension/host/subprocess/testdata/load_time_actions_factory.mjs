export default async function (pi) {
  const out = {};
  const probe = async (name, call) => {
    try { const v = call(); if (v && typeof v.then === "function") { try { out[name] = "resolved:" + JSON.stringify(await v); } catch (e) { out[name] = "rejected:" + e.message; } } else out[name] = "returned:" + JSON.stringify(v); }
    catch (e) { out[name] = "threw:" + e.message; }
  };
  await probe("sendMessage", () => pi.sendMessage({ customType: "x", content: "y" }));
  await probe("sendUserMessage", () => pi.sendUserMessage("hi"));
  await probe("appendEntry", () => pi.appendEntry("t", {}));
  await probe("setSessionName", () => pi.setSessionName("n"));
  await probe("getSessionName", () => pi.getSessionName());
  await probe("setLabel", () => pi.setLabel("id", "l"));
  await probe("getActiveTools", () => pi.getActiveTools());
  await probe("getAllTools", () => pi.getAllTools());
  await probe("getSettings", () => pi.getSettings());
  await probe("setActiveTools", () => pi.setActiveTools(["read"]));
  await probe("getCommands", () => pi.getCommands());
  await probe("setModel", () => pi.setModel({ provider: "p", id: "m" }));
  await probe("getThinkingLevel", () => pi.getThinkingLevel());
  await probe("setThinkingLevel", () => pi.setThinkingLevel("low"));
  await probe("getMcpServers", () => pi.getMcpServers());
  await probe("events.emit", () => pi.events.emit("ch", 1));
  await probe("events.on", () => typeof pi.events.on("ch", () => {}));
  await probe("unregisterVirtualModel", () => pi.unregisterVirtualModel("p", "id"));
  await probe("exec", () => pi.exec("true", []).then((r) => r.code));
  globalThis.__probeOut = out;
  pi.registerCommand("probe", { description: JSON.stringify(out), handler: async () => {} });
}
