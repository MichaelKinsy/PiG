// session_start appends an entry, names the session and notifies, each carrying how many times this extension instance has run session_start; session_shutdown notifies.
// Pi builds a fresh instance for every Session (the factory runs again), and so does pig (its replacement's extensions are new processes): a second session_start on one
// Session would count 2 in both. The count is an input no default can produce.
export default function (pi) {
  let starts = 0;
  pi.on("session_start", (event, ctx) => {
    starts++;
    pi.appendEntry("probe-start", { phase: event.reason, starts });
    pi.setSessionName("start-name-" + event.reason + "-" + starts);
    ctx.ui.notify("start-notify-" + event.reason + "-" + starts, "info");
  });
  pi.on("session_shutdown", (event, ctx) => {
    ctx.ui.notify("shutdown-notify-" + event.reason, "info");
  });
}
