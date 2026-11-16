// parity-exec-close-order: when pi.exec() closes the command's output pipes in relation to its SIGTERM (core/exec.ts:76-98, :111-113).
// /exec-close-order-probe runs three shells that trap SIGTERM with a handler that writes "caught" or "bye" and exits with a status of its own, and
// notifies each result in full:
//   cap:     maxBuffer 3, and the shell has written 5 bytes. The chunk that crosses the cap destroys both pipes and then signals (exec.ts:111-113),
//            so the handler's write meets a closed pipe and the shell dies of SIGPIPE: code 141, and "caught" is never read.
//   timeout: the timeout fires; killProcess sends SIGTERM and leaves the pipes open (exec.ts:96-98), so the handler's "bye" is read and the shell
//            exits 4, with killed set.
//   abort:   the same by an abort signal, raised once the shell has trapped SIGTERM and written "ready" (it creates a file for the poll), exit 4.
import { existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const sh = (script, ...rest) => ["-c", script, "sh", ...rest];
const show = (label, r) =>
  `exec-close-order ${label}: code=${r.code} killed=${r.killed} truncated=${r.truncated} stdout=${JSON.stringify(r.stdout)} stderr=${JSON.stringify(r.stderr)}`;

export default function (pi) {
  pi.registerCommand("exec-close-order-probe", {
    description: "Run SIGTERM-trapping shells through pi.exec under a cap, a timeout and an abort, and report each result.",
    handler: async (_args, ctx) => {
      const cap = await pi.exec("sh", sh("trap 'echo caught; exit 3' TERM; printf 12345; while :; do sleep 0.05; done"), { maxBuffer: 3 });
      ctx.ui.notify(show("cap", cap), "info");
      const timed = await pi.exec("sh", sh("trap 'echo bye; exit 4' TERM; echo ready; while :; do sleep 0.05; done"), { timeout: 1500 });
      ctx.ui.notify(show("timeout", timed), "info");
      const ready = join(mkdtempSync(join(tmpdir(), "exec-close-order-")), "ready");
      const controller = new AbortController();
      const watch = setInterval(() => {
        if (existsSync(ready)) controller.abort();
      }, 5);
      const aborted = await pi.exec("sh", sh("trap 'echo bye; exit 4' TERM; echo ready; : > \"$1\"; while :; do sleep 0.05; done", ready), {
        signal: controller.signal,
      });
      clearInterval(watch);
      ctx.ui.notify(show("abort", aborted), "info");
    },
  });
}
