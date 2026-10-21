// Pi rpc-mode.ts:195-208: setWidget forwards undefined and string arrays; a component factory is ignored and never run.
export default function (pi) {
  pi.registerCommand("widget-kinds", {
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("lines", ["string-widget-line"]);
      ctx.ui.setWidget("factory", () => {
        ctx.ui.notify("factory-ran", "info");
        return { render: () => ["FACTORY-FRAME"], invalidate() {} };
      });
      ctx.ui.setWidget("factory", undefined);
      ctx.ui.setWidget("lines", undefined);
    },
  });
}
