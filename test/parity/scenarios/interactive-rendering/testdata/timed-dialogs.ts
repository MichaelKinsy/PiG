export default function (pi: any) {
  pi.registerCommand("timed-dialogs", {
    description: "Let timed extension dialogs expire without a provider request",
    handler: async (_args: string, ctx: any) => {
      const picked = await ctx.ui.select("Timed pick", ["first", "second"], { timeout: 1500 });
      const confirmed = await ctx.ui.confirm("Timed confirm", "Sure?", { timeout: 1000 });
      const entered = await ctx.ui.input("Timed name", "", { timeout: 1000 });
      ctx.ui.notify(`Timed results ${JSON.stringify({ picked: picked ?? null, confirmed, entered: entered ?? null })}`, "info");
    },
  });
}
