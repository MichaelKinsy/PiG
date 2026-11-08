#!/usr/bin/env node
// PiG's harness entry, process.argv[1] in a Node extension's process
// (process-identity.mjs). Pi extensions re-launch the harness with
// `process.execPath process.argv[1] ...args`; this runs the PiG binary that
// started the extension with those arguments, the same stdio, and its exit
// status. package.json names the package the way Pi's CLI entry's does, which
// is how extensions (pi-subagents) recognize a Pi entry.
//
// Pi's entry is the agent itself, so SIGKILL of the entry ends the agent. Here
// the entry is a wrapper, and SIGKILL cannot be forwarded: a small watchdog
// (this file run again with PIG_HARNESS_WATCHDOG set) holds the read end of a
// pipe whose write end only the entry holds, and kills the binary when the
// pipe closes without the entry having stood the watchdog down.
import { execFileSync, spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import { exitAfterStderr } from "../exit-after-stderr.mjs";

const signals = ["SIGINT", "SIGTERM", "SIGHUP"];

if (process.env.PIG_HARNESS_WATCHDOG) {
  watch(Number(process.env.PIG_HARNESS_WATCHDOG));
} else {
  run();
}

// The entry's exit closes stdin; a clean exit has killed this process first. If the binary ended on its own as the entry died, its pid may be free again: the kill goes only to a process that started when the binary did.
function watch(pid) {
  for (const signal of signals) process.on(signal, () => {});
  const started = startTime(pid);
  if (started === undefined) process.exit(0);
  let stopped = false;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    if (startTime(pid) === started) {
      try {
        process.kill(pid, "SIGKILL");
      } catch {}
    }
    process.exit(0);
  };
  process.stdin.on("end", stop);
  process.stdin.on("error", stop);
  process.stdin.resume();
}

// A value that tells this process apart from a later one with the same pid, undefined when pid is not running. Windows has no cheap equivalent: the pid alone identifies it there.
function startTime(pid) {
  try {
    if (process.platform === "win32") return "";
    if (process.platform === "linux") {
      // /proc/<pid>/stat field 22 (starttime); the command name in field 2 may hold spaces and parentheses, so count from the last ")".
      const stat = readFileSync(`/proc/${pid}/stat`, "utf8");
      return stat.slice(stat.lastIndexOf(")") + 2).split(" ")[19];
    }
    return execFileSync("ps", ["-o", "lstart=", "-p", String(pid)], { encoding: "utf8" }).trim() || undefined;
  } catch {
    return undefined;
  }
}

function run() {
  const binary = process.env.PIG_HARNESS_BINARY;
  if (!binary) {
    exitAfterStderr("PIG_HARNESS_BINARY is not set: this entry runs the PiG binary that started the extension");
    return;
  }
  const child = spawn(binary, process.argv.slice(2), { stdio: "inherit" });
  const forward = (signal) => child.kill(signal);
  for (const signal of signals) process.on(signal, forward);
  let watchdog;
  if (child.pid !== undefined) {
    watchdog = spawn(process.execPath, [process.argv[1]], {
      stdio: ["pipe", "ignore", "ignore"],
      env: { ...process.env, PIG_HARNESS_WATCHDOG: String(child.pid) },
    });
    watchdog.on("error", () => {});
    watchdog.stdin.on("error", () => {});
  }
  child.on("error", (error) => {
    watchdog?.kill("SIGKILL");
    exitAfterStderr(`${binary}: ${error.message}`);
  });
  child.on("exit", (code, signal) => {
    watchdog?.kill("SIGKILL");
    if (signal) {
      for (const name of signals) process.off(name, forward);
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 1);
  });
}
