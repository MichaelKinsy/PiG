import { appendFileSync, readFileSync, writeFileSync } from "node:fs";

// The first session_start returns; the second (a connection's first bind) writes LISTEN_BUILD_MARKER and never returns.
export default function (pi) {
  pi.on("session_start", async () => {
    const counter = process.env.LISTEN_BUILD_COUNT;
    let loads = 0;
    try { loads = readFileSync(counter, "utf8").length; } catch {}
    appendFileSync(counter, "x");
    if (loads === 0) return;
    writeFileSync(process.env.LISTEN_BUILD_MARKER, "starting");
    await new Promise(() => {});
  });
}
