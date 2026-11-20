#!/usr/bin/env node
// PiG's harness entry, process.argv[1] in a Node extension's process
// (process-identity.mjs). Pi extensions re-launch the harness with
// `process.execPath process.argv[1] ...args`; this runs the PiG binary that
// started the extension with those arguments, the same stdio, and its exit
// status. package.json names the package the way Pi's CLI entry's does, which
// is how extensions (pi-subagents) recognize a Pi entry.
//
// Pi's entry is the agent itself, so SIGKILL of the entry ends the agent. Here
// the entry is a wrapper and SIGKILL cannot be forwarded, so the entry never
// starts the binary: it starts a supervisor (supervisor.mjs) that does, and
// that kills the binary when its IPC channel to the entry closes. No instant
// has a running binary without a supervisor to end it, whenever and however the
// entry dies. Signals sent to the entry reach the binary through the channel.
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { exitAfterStderr } from "../exit-after-stderr.mjs";
import { endLike, signals } from "./relay.mjs";

run();

function run() {
  if (!process.env.PIG_HARNESS_BINARY) {
    exitAfterStderr("PIG_HARNESS_BINARY is not set: this entry runs the PiG binary that started the extension");
    return;
  }
  const supervisor = spawn(process.execPath, [fileURLToPath(new URL("./supervisor.mjs", import.meta.url)), ...process.argv.slice(2)], {
    stdio: ["inherit", "inherit", "inherit", "ipc"],
  });
  const forward = (signal) => supervisor.connected && supervisor.send({ signal }, () => {});
  for (const signal of signals) process.on(signal, forward);
  supervisor.on("error", (error) => exitAfterStderr(`${process.execPath}: ${error.message}`));
  supervisor.on("exit", (code, signal) => endLike(code, signal, forward));
}
