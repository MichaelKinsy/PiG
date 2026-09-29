// Minimal test extension that exercises ctx.ui.input() and ctx.ui.editor().
// Used by parity scenarios to assert extension-input and extension-editor
// overlays without requiring a live LLM.
//
// Commands registered:
//   /probe-input  : opens ctx.ui.input("Probe Title", "placeholder")
//   /probe-editor : opens ctx.ui.editor("Probe Editor Title", "")
//
// After submit/cancel the result is printed via ctx.ui.notify so it's
// visible in the tmux capture.

export default function (pi: any) {
  pi.registerCommand("probe-input", {
    description: "Open ctx.ui.input dialog for parity testing",
    handler: async (_args: string, ctx: any) => {
      const result = await ctx.ui.input("Probe Title", "type here");
      if (result === undefined) {
        ctx.ui.notify("probe-input: cancelled", "info");
      } else {
        ctx.ui.notify(`probe-input: ${result}`, "info");
      }
    },
  });

  pi.registerCommand("probe-editor", {
    description: "Open ctx.ui.editor dialog for parity testing",
    handler: async (_args: string, ctx: any) => {
      const result = await ctx.ui.editor("Probe Editor Title", "");
      if (result === undefined) {
        ctx.ui.notify("probe-editor: cancelled", "info");
      } else {
        ctx.ui.notify(`probe-editor: ${result}`, "info");
      }
    },
  });
}
