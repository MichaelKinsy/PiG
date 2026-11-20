// A test preload (node --import): writes a line to stderr whenever a Node process of the harness entry raises a signal in HARNESS_MARK_SIGNALS (platform signal numbers) on itself with process.kill. Nothing else
// shows it: Node ignores SIGPIPE and SIGXFSZ, so a raised one leaves no trace, and the notice SIGUSR1's inspector writes may come after the process has ended.
import { constants } from "node:os";

const numbers = (process.env.HARNESS_MARK_SIGNALS ?? "").split(",").map(Number);
const kill = process.kill;
process.kill = function (pid, signal) {
  if (pid === process.pid && numbers.includes(typeof signal === "number" ? signal : constants.signals[signal])) process.stderr.write(`${signal} raised on pid ${pid} by itself\n`);
  return kill.call(this, pid, signal);
};
