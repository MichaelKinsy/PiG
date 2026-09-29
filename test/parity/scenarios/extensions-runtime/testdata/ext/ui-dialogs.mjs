export default function (pi) {
  pi.registerTool({
    name: "ui_dialog_probe",
    description: "Exercise subprocess extension dialog calls.",
    parameters: { type: "object", properties: {} },
    async execute(_toolCallId, _params, _signal, _onUpdate, ctx) {
      const selected = await ctx.ui.select("Pick a deployment", ["staging", "production"]);
      if (selected === undefined) return { content: [{ type: "text", text: "dialogs-cancelled:select" }], details: {} };

      const input = await ctx.ui.input("Deployment owner", "name");
      if (input === undefined) return { content: [{ type: "text", text: "dialogs-cancelled:input" }], details: {} };

      const edited = await ctx.ui.editor("Deployment note", "note:");
      if (edited === undefined) return { content: [{ type: "text", text: "dialogs-cancelled:editor" }], details: {} };

      const text = `dialogs-ok:${selected}|${input}|${edited}`;
      return { content: [{ type: "text", text }], details: {} };
    },
  });
}
