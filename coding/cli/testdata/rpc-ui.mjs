export default function rpcUIExtension(pi) {
  let blockNextModelSelect = false;
  let blockNextSessionSwitch = false;
  let blockNextCompaction = false;

  pi.on("input", (event, ctx) => {
    if (event.text !== "handled input") return { action: "continue" };
    ctx.ui.notify(`input:${event.source}:${event.streamingBehavior ?? "idle"}`, "info");
    return { action: "handled" };
  });

  pi.on("session_info_changed", (event, ctx) => {
    ctx.ui.notify(`session-name:${event.name ?? "none"}`, "info");
  });

  pi.on("session_before_compact", async (_event, ctx) => {
    if (!blockNextCompaction) return;
    blockNextCompaction = false;
    await ctx.ui.select("Compacting", ["continue"]);
  });

  pi.registerCommand("block-compaction", {
    description: "Block the next compaction on RPC UI input",
    handler: async () => {
      blockNextCompaction = true;
    },
  });

  pi.on("session_before_switch", async (_event, ctx) => {
    if (!blockNextSessionSwitch) return;
    blockNextSessionSwitch = false;
    await ctx.ui.select("Session switching", ["continue"]);
  });

  pi.registerCommand("block-session-switch", {
    description: "Block the next Session switch on RPC UI input",
    handler: async () => {
      blockNextSessionSwitch = true;
    },
  });

  pi.on("model_select", async (_event, ctx) => {
    if (!blockNextModelSelect) return;
    blockNextModelSelect = false;
    await ctx.ui.select("Model selected", ["continue"]);
  });

  pi.registerCommand("block-model-select", {
    description: "Block the next model selection on RPC UI input",
    handler: async () => {
      blockNextModelSelect = true;
    },
  });

  pi.registerCommand("show", {
    description: "Exercise fire-and-forget RPC extension UI calls",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("rpc-ui", "ready");
      ctx.ui.setWidget("choice", ["selected:none"], { placement: "belowEditor" });
      ctx.ui.notify("selected:none", "info");
      ctx.ui.setTitle("RPC UI");
      ctx.ui.setEditorText("next prompt");
    },
  });

  pi.registerCommand("session-events", {
    description: "Exercise RPC session entry and information events",
    handler: async () => {
      pi.appendEntry("rpc-entry", { value: "hello" });
      pi.setSessionName("extension-name");
    },
  });

  pi.registerCommand("clear-session-name", {
    description: "Exercise an empty extension-defined session name",
    handler: async () => {
      pi.setSessionName("");
    },
  });

  pi.registerCommand("ask", {
    description: "Exercise the RPC extension UI transport",
    handler: async (_args, ctx) => {
      const selected = await ctx.ui.select("Choose", ["A", "B"]);
      ctx.ui.setWidget("choice", [`selected:${selected ?? "none"}`], { placement: "belowEditor" });
      ctx.ui.notify(`selected:${selected ?? "none"}`, "info");
    },
  });
}
