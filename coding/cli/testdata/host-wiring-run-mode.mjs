// host-wiring-run-mode.mjs records the ctx.mode its session_start handler reads. Pi binds a mode's extensions with that mode (print-mode.ts: "json" or "print", rpc-mode.ts: "rpc").
import { appendFileSync } from "node:fs";

export default function hostWiringRunMode(pi) {
  pi.on("session_start", (_event, ctx) => {
    appendFileSync(process.env.WIRING_REPORT, JSON.stringify({ event: "mode", value: ctx.mode }) + "\n");
  });
}
