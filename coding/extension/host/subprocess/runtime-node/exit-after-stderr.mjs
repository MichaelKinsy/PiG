import { format } from "node:util";

// Ends the process with exit code 1 once stderr has taken the report. The host reads stderr through a pipe, and Node writes pipes asynchronously on POSIX: process.exit right after console.error drops whatever the pipe had no room for, so the extension would die without its reason.
export function exitAfterStderr(...report) {
  process.stderr.write(format(...report) + "\n", () => process.exit(1));
}
