import fs from "node:fs";

// Calls ctx.shutdown() from a turn_start handler, while the run streams. Pi ends the session when the run settles (interactive-mode.ts agent_settled ->
// checkShutdownRequested), so the reply is drawn first. The "quit" session_shutdown reports on the controlling terminal after the TUI has stopped.
export default function (pi) {
  pi.on("session_shutdown", (event) => {
    fs.writeFileSync("/dev/tty", `SHUTDOWN_EVENT ${event.reason}\n`);
  });
  pi.on("turn_start", (_event, ctx) => {
    ctx.shutdown();
  });
}
