export default function(pi) {
  pi.registerCommand("subscribe", { handler: (_args, ctx) => {
    ctx.ui.onTerminalInput(data => {
      if (data === "rewrite-high") return { data: "\ud83d" };
      if (data === "rewrite-low") return { data: "\ude00" };
      const units = s => Array.from({ length: s.length }, (_, i) => s.charCodeAt(i));
      return { data: JSON.stringify({ units: units(data), editor: units(ctx.ui.getEditorText()) }) };
    });
  }});
}
