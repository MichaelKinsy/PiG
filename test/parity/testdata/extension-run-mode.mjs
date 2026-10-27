// extension-run-mode.mjs records the ctx.mode its session_start handler reads, for the run-mode scenario that runs Pi and PiG alike.
import { appendFileSync } from "node:fs";

export default function extensionRunMode(pi) {
  pi.on("session_start", (_event, ctx) => {
    appendFileSync(process.env.RUN_MODE_REPORT, ctx.mode + "\n");
  });
}
