// Pi rpc-mode.ts:151-210 forwards these synchronous UI calls in invocation order.
export default function (pi) {
  pi.registerCommand("wire-ui", {
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("wire", "active");
      ctx.ui.setStatus("wire", undefined);
      ctx.ui.setWidget("wire", ["active"]);
      ctx.ui.setWidget("wire", undefined);
    },
  });
}
