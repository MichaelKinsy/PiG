// session-start-once.mjs is the extension of Pi's 10470-rpc-session-replacement-start-once.test.ts: its session_start handler appends a "start-marker"
// entry whose data is `${event.reason}:${starts so far}` and counts the call. Pi's test keeps the counter in the factory's closure, which every Session's
// factory run shares; here each Session's extensions run in their own process, so the calls are lines of the file PIG_TEST_START_LOG.
import { appendFileSync, existsSync, readFileSync } from "node:fs";

export default function (pi) {
  pi.on("session_start", (event) => {
    const log = process.env.PIG_TEST_START_LOG;
    const starts = existsSync(log) ? readFileSync(log, "utf8").split("\n").filter((line) => line !== "").length : 0;
    const marker = `${event.reason}:${starts}`;
    appendFileSync(log, `${marker}\n`);
    pi.appendEntry("start-marker", marker);
  });
}
