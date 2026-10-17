import fs from "node:fs";

// Calls ctx.shutdown() from the awaited handler named by SHUTDOWN_AT and never
// settles that handler, so the host must exit without waiting for it. A
// session_start handler acts only for SHUTDOWN_REASON (default "startup").
// The "quit" session_shutdown runs after the TUI has stopped, so it reports on
// the controlling terminal, which Pi's process and PiG's extension process share.
export default function (pi) {
  const at = process.env.SHUTDOWN_AT;
  pi.on("session_shutdown", (event) => {
    fs.writeFileSync("/dev/tty", `SHUTDOWN_EVENT ${event.reason}\n`);
  });
  pi.on(at, (event, ctx) => {
    if (at === "session_start" && event.reason !== (process.env.SHUTDOWN_REASON || "startup")) return;
    ctx.shutdown();
    return new Promise(() => {});
  });
}
