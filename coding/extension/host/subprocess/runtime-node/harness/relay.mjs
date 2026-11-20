// What the harness entry and its supervisor share: the signals the entry forwards to the binary, and ending as the process they ran ended.
export const signals = ["SIGINT", "SIGTERM", "SIGHUP"];

// The signals that end a process by default and that Node does not end itself with when one is raised on it: it ignores SIGPIPE and SIGXFSZ, and takes SIGUSR1 for its inspector. The list is what TestHarnessEntryNeverRaisesASignalNodeSurvives
// finds Node does on the platform it runs on: it fails for a signal Node does not die from that is missing here and for one Node dies from that is listed.
export const survivedSignals = ["SIGPIPE", "SIGXFSZ", "SIGUSR1"];

// Ends this process as the process it ran ended: by the same signal, once the handlers that kept it alive are off so the signal's default action applies, or with the same exit code. A signal Node does not die from is not raised: raising
// it would only open an inspector (SIGUSR1) that writes a notice and can crash Node while it opens, and the process ends with code 0 as it would after the raise, which is how the entry ended before it had a supervisor.
export function endLike(code, signal, handler) {
  if (!signal) {
    process.exit(code ?? 1);
    return;
  }
  for (const name of signals) process.off(name, handler);
  if (!survivedSignals.includes(signal)) process.kill(process.pid, signal);
  // Reached when the signal was not raised or did not end this process. A process with nothing left to wait for then ends with code 0; the IPC channel to the other hop is the one thing left to wait for, so close it.
  if (process.connected) process.disconnect();
}
