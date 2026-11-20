// Fixture for harness_cli_kill_test.go, loaded into every harness process through NODE_OPTIONS=--import. HARNESS_STALL_AT names the point at which one process
// writes the pid the test waits to see to HARNESS_STALL_FLAG and then waits for the file HARNESS_STALL_RELEASE to exist:
//   entry-after-first-spawn       the entry, right after it started its first child (that child's pid), before it installs a signal handler or starts anything else; blocked
//   supervisor-before-spawn       the supervisor, right before it starts the binary (its own pid); blocked. The entry appends every message it sends its supervisor to HARNESS_STALL_FLAG.sent
//   supervisor-after-binary-exit  the supervisor, when it asks to end because its binary ended (its own pid); its event loop keeps running while it waits
// In both supervisor modes every signal the supervisor sends is appended to HARNESS_STALL_FLAG.calls as "child.kill <signal> <result>" or "process.kill <pid> <signal>".
import cp from "node:child_process";
import { appendFileSync, existsSync, renameSync, writeFileSync } from "node:fs";
import { syncBuiltinESMExports } from "node:module";

const { HARNESS_STALL_AT: at, HARNESS_STALL_FLAG: flag, HARNESS_STALL_RELEASE: release } = process.env;
const supervisor = process.argv[1].endsWith("supervisor.mjs");

function stall(pid) {
  writeFileSync(`${flag}.tmp`, String(pid));
  renameSync(`${flag}.tmp`, flag);
  const cell = new Int32Array(new SharedArrayBuffer(4));
  while (!existsSync(release)) Atomics.wait(cell, 0, 0, 20);
}

const spawn = cp.spawn;
let first = true;
cp.spawn = (...args) => {
  if (supervisor && at === "supervisor-before-spawn") stall(process.pid);
  const child = spawn(...args);
  if (!supervisor && at === "entry-after-first-spawn" && first) stall(child.pid);
  if (!supervisor && at === "supervisor-before-spawn" && child.send) {
    const send = child.send;
    child.send = (...rest) => {
      const sent = send.apply(child, rest);
      appendFileSync(`${flag}.sent`, `${JSON.stringify(rest[0])}\n`);
      return sent;
    };
  }
  first = false;
  return child;
};
syncBuiltinESMExports();

if (supervisor && (at === "supervisor-before-spawn" || at === "supervisor-after-binary-exit")) {
  const calls = `${flag}.calls`;
  const childKill = cp.ChildProcess.prototype.kill;
  cp.ChildProcess.prototype.kill = function (signal) {
    const result = childKill.call(this, signal);
    appendFileSync(calls, `child.kill ${signal} ${result}\n`);
    return result;
  };
  const processKill = process.kill;
  process.kill = (pid, signal) => {
    appendFileSync(calls, `process.kill ${pid} ${signal}\n`);
    return processKill.call(process, pid, signal);
  };
}

if (supervisor && at === "supervisor-after-binary-exit") {
  const exit = process.exit;
  process.exit = (code) => {
    writeFileSync(`${flag}.tmp`, String(process.pid));
    renameSync(`${flag}.tmp`, flag);
    const timer = setInterval(() => {
      if (!existsSync(release)) return;
      clearInterval(timer);
      exit.call(process, code);
    }, 10);
  };
}
