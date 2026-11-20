// The harness entry as it ended its binary before it had a supervisor, and the observable the supervised entry keeps: the binary's exit code, or the binary's signal raised on the entry itself
// (which Node does not die from for SIGPIPE and SIGXFSZ, and takes SIGUSR1 for its inspector: the entry then ends with code 0 once nothing is left to wait for).
// TestHarnessEntryEndsAsItsBinaryEndedForEveryExitAndSignal runs it beside runtime-node/harness/cli.mjs.
import { spawn } from "node:child_process";

const signals = ["SIGINT", "SIGTERM", "SIGHUP"];
const child = spawn(process.env.PIG_HARNESS_BINARY, process.argv.slice(2), { stdio: "inherit" });
const forward = (signal) => child.kill(signal);
for (const signal of signals) process.on(signal, forward);
child.on("exit", (code, signal) => {
  if (signal) {
    for (const name of signals) process.off(name, forward);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
