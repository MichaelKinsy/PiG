// Pi rpc-mode.ts createDialogPromise (lines 91-131): a signal that is already aborted resolves the default with no request, and a signal that aborts later resolves the default and drops the pending request.
export default function (pi) {
  pi.registerCommand("dialog-signal", {
    handler: async (_args, ctx) => {
      const early = new AbortController();
      early.abort();
      const pre = [
        await ctx.ui.select("pre-select", ["a"], { signal: early.signal }),
        await ctx.ui.confirm("pre-confirm", "m", { signal: early.signal }),
        await ctx.ui.input("pre-input", "p", { signal: early.signal }),
      ];
      ctx.ui.notify("pre:" + JSON.stringify(pre), "info");
      const late = new AbortController();
      setTimeout(() => late.abort(), 100);
      const selected = await ctx.ui.select("late-select", ["a", "b"], { signal: late.signal });
      ctx.ui.notify("late:" + JSON.stringify(selected ?? null), "info");
      const later = new AbortController();
      setTimeout(() => later.abort(), 100);
      const confirmed = await ctx.ui.confirm("late-confirm", "m", { signal: later.signal });
      ctx.ui.notify("later:" + confirmed, "info");
    },
  });
}
