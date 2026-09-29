// Pi interactive-mode.ts:1008-1015,2467-2488 owns spacers outside the header component.
export default function(pi) {
  let lines = ["HEADER INITIAL"];
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setHeader(() => ({ render: () => lines, invalidate() {} }));
    ctx.ui.setFooter(() => ({ render: () => ["HEADER PROBE READY"], invalidate() {} }));
  });
  pi.registerCommand("header-swap", {handler: (_args, ctx) => {
    lines = ["HEADER REPLACED", "", "HEADER LAST"];
    ctx.ui.setHeader(() => ({render: () => lines, invalidate() {}}));
  }});
}
