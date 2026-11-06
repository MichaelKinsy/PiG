// A session_before_tree handler that awaits, as an extension that asks something before a tree navigation does. The navigation
// cannot finish before it returns, so the time between the summary prompt closing and the new transcript is a known 600 ms.
export default function (pi) {
  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.notify("SLOW HOOK READY", "info");
  });
  pi.on("session_before_tree", async () => {
    await new Promise((resolve) => setTimeout(resolve, 600));
  });
}
