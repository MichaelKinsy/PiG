import { appendFileSync, existsSync, writeFileSync } from "node:fs";

// A third extension file whose qcrash command crashes the shared Node process once, so the host quarantines it into a Node process of its own, apart from rpc-shutdown.mjs's quit handler. The Pi leg creates the marker first and never sends qcrash: Pi runs every extension in one process and has no crash to recover from. Each load of the factory records qloaded.
export default function (pi) {
  const marker = process.env.RPC_SHUTDOWN_CRASH_MARKER;
  const record = (name) => appendFileSync(process.env.RPC_SHUTDOWN_REPORT, name + "\n");
  record("qloaded");
  pi.registerCommand("qcrash", {
    description: "Crash the Node process once",
    handler: async () => {
      if (marker && !existsSync(marker)) {
        writeFileSync(marker, "crashed");
        process.exit(29);
      }
    },
  });
  // qping records the mode of this extension's runtime, which pig reports and Pi does not: a restarted Node process serves commands before the host activates it, and until then its mode is not "rpc" and it runs a command without the RPC invocation acknowledgment.
  pi.registerCommand("qping", {
    description: "Record the runtime mode",
    handler: async (_args, ctx) => record("qping:" + ctx.mode),
  });
  // qask waits on a select that nothing answers after stdin ended.
  pi.registerCommand("qask", {
    description: "Wait on a select",
    handler: async (_args, ctx) => {
      await ctx.ui.select("QAsk", ["yes"]);
    },
  });
  // qhang waits on a Promise that nothing can settle and that keeps no handle alive.
  pi.registerCommand("qhang", {
    description: "Wait on a Promise nothing settles",
    handler: async () => {
      await new Promise(() => {});
    },
  });
  // qns replaces the Session, so the response follows the host's replacement build.
  pi.registerCommand("qns", {
    description: "Replace the Session",
    handler: async (_args, ctx) => {
      await ctx.newSession();
      record("qns-done");
    },
  });
  // qexec waits on a child process, which keeps the loop alive.
  pi.registerCommand("qexec", {
    description: "Wait on a child process",
    handler: async () => {
      await pi.exec("sleep", ["2"]);
      record("qexec-done");
    },
  });
}
