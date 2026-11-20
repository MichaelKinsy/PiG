// What the harness entry and its supervisor share: the signals the entry forwards to the binary, and ending as the process they ran ended.
export const signals = ["SIGINT", "SIGTERM", "SIGHUP"];

// Ends this process as the process it ran ended: by the same signal, once the handlers that kept it alive are off so the signal's default action applies, or with the same exit code.
export function endLike(code, signal, handler) {
  if (signal) {
    for (const name of signals) process.off(name, handler);
    process.kill(process.pid, signal);
    // Reached only when the signal did not end this process: Node ignores SIGPIPE and SIGXFSZ and takes SIGUSR1 for its inspector. A process with nothing left to wait for then ends with code 0, which is
    // how the entry ended before it had a supervisor; the IPC channel to the other hop is the one thing left to wait for, so close it.
    if (process.connected) process.disconnect();
    return;
  }
  process.exit(code ?? 1);
}
