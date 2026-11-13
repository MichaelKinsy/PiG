// Scratch fixture for the setModel-object probe: pi.setModel receives the whole model object the command carries as JSON, as Pi's ExtensionAPI.setModel(model: Model<any>) does.
export default function (pi) {
  pi.registerCommand("set_model_object", {
    description: "pi.setModel(JSON.parse(args))",
    handler: async (args, ctx) => {
      const ok = await pi.setModel(JSON.parse(args));
      ctx.ui.notify(`setModelObject=${ok}`, "info");
    },
  });
  pi.on("model_select", async (event, ctx) => {
    const m = event.model;
    ctx.ui.notify(`model_select:${event.source}:${m.provider}/${m.id}:cw=${m.contextWindow}:base=${m.baseUrl}`, "info");
  });
}
