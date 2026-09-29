// Follows Pi's examples/extensions/confirm-destructive.ts: session_before_switch
// and session_before_fork ask through ctx.ui and cancel on a refusal.
export default function (pi) {
  const cancellations = [];
  const reportCancellation = (ctx, message) => {
    cancellations.push(message);
    ctx.ui.notify(cancellations.join("\n"), "info");
  };
  pi.on("session_before_switch", async (event, ctx) => {
    const confirmed = await ctx.ui.confirm("Clear session?", `reason=${event.reason}`);
    if (!confirmed) {
      reportCancellation(ctx, `Switch cancelled reason=${event.reason}`);
      return { cancel: true };
    }
  });
  pi.on("session_before_fork", async (event, ctx) => {
    const choice = await ctx.ui.select(`Fork position=${event.position}?`, ["Yes, create fork", "No, stay"]);
    if (choice !== "Yes, create fork") {
      reportCancellation(ctx, `Fork cancelled position=${event.position}`);
      return { cancel: true };
    }
  });
}
