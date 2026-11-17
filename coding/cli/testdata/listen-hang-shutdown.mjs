import { appendFileSync } from "node:fs";

// Records that session_shutdown reached the handler in LISTEN_SHUTDOWN_REPORT, then blocks the runtime's event loop for good: the session's dispose hangs and only killing the extension process ends it.
export default function (pi) {
  pi.on("session_shutdown", async () => {
    appendFileSync(process.env.LISTEN_SHUTDOWN_REPORT, "session_shutdown\n");
    for (;;);
  });
}
