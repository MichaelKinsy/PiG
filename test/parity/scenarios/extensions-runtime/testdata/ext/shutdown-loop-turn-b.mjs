// The second of two extensions in a print-mode disposal. Its session_shutdown handler awaits a
// timer, which turns Pi's event loop before the runner is invalidated.
import { appendFileSync } from "node:fs";

export default function (pi) {
  const record = (line) => appendFileSync(process.env.LOOP_TURN_LOG, `${line}\n`);
  pi.on("session_shutdown", async () => {
    record("b session_shutdown");
    await new Promise((resolve) => setTimeout(resolve, 50));
    record("b session_shutdown done");
  });
}
