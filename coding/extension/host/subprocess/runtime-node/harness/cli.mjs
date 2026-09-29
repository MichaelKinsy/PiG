#!/usr/bin/env node
// PiG's harness entry, process.argv[1] in a Node extension's process
// (process-identity.mjs). Pi extensions re-launch the harness with
// `process.execPath process.argv[1] ...args`; this runs the PiG binary that
// started the extension with those arguments, the same stdio, and its exit
// status. package.json names the package the way Pi's CLI entry's does, which
// is how extensions (pi-subagents) recognize a Pi entry.
import { spawn } from "node:child_process";

const binary = process.env.PIG_HARNESS_BINARY;
if (!binary) {
  console.error("PIG_HARNESS_BINARY is not set: this entry runs the PiG binary that started the extension");
  process.exit(1);
}

const signals = ["SIGINT", "SIGTERM", "SIGHUP"];
const child = spawn(binary, process.argv.slice(2), { stdio: "inherit" });
const forward = (signal) => child.kill(signal);
for (const signal of signals) process.on(signal, forward);
child.on("error", (error) => {
  console.error(`${binary}: ${error.message}`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  if (signal) {
    for (const name of signals) process.off(name, forward);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
