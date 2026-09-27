export default function (pi) {
  pi.registerCommand("error-probe", {
    handler: async (args, ctx) => {
      if (args === "fail") throw new Error("connection closed");
      ctx.ui.notify(`error-probe-${args}`, "info");
    },
  });
}
