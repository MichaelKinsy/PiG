// Pi rpc-mode.ts:195-206 and notify (137-150): every setWidget call for undefined or a string array writes a request, in call order among the other UI requests; an identical repeat and a set after a clear are written too.
export default function (pi) {
  pi.registerCommand("widget-order", {
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("w", ["w-one"]);
      ctx.ui.notify("w-n1", "info");
      ctx.ui.setWidget("w", ["w-one"]);
      ctx.ui.setWidget("w", undefined);
      ctx.ui.setWidget("w", ["w-two"]);
      ctx.ui.notify("w-n2", "info");
    },
  });
}
