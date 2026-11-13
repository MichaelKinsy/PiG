// parity-exec-signal: pi.exec() of a command that a signal ends (core/exec.ts; Pi a83b75a07, #12089).
// /exec-signal-probe runs three commands and notifies each result's code and killed flag:
//   term:    sh -c 'kill -TERM $$'   the command ends itself with SIGTERM (15); pi.exec sent nothing
//   kill:    sh -c 'kill -KILL $$'   the same with SIGKILL (9), so the code is 128 + the signal's number and not one constant
//   timeout: sleep 30, timeout 200 ms: pi.exec sends SIGTERM when the timeout fires
export default function (pi) {
  pi.registerCommand("exec-signal-probe", {
    description: "Run commands that a signal ends via pi.exec and report their codes.",
    handler: async (_args, ctx) => {
      const term = await pi.exec("sh", ["-c", "kill -TERM $$"]);
      ctx.ui.notify(`exec-signal term: code=${term.code} killed=${term.killed}`, "info");
      const kill = await pi.exec("sh", ["-c", "kill -KILL $$"]);
      ctx.ui.notify(`exec-signal kill: code=${kill.code} killed=${kill.killed}`, "info");
      const timed = await pi.exec("sleep", ["30"], { timeout: 200 });
      ctx.ui.notify(`exec-signal timeout: code=${timed.code} killed=${timed.killed}`, "info");
    },
  });
}
