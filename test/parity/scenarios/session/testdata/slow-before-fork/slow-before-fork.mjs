// A session_before_fork handler that awaits, as an extension that asks something before a fork does. The swap cannot finish before it returns,
// so the time between the picker closing and the new transcript is a known 400 ms.
export default function (pi) {
  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.notify("SLOW HOOK READY", "info");
  });
  pi.on("session_before_fork", async () => {
    await new Promise((resolve) => setTimeout(resolve, 400));
  });
}
