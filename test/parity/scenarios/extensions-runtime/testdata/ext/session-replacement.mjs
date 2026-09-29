import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { resolve } from "node:path";

export default function (pi) {
  pi.registerCommand("replacement-report", {
    handler: async (path, ctx) => ctx.ui.notify("replacement-trace:" + readFileSync(resolve(ctx.cwd, path), "utf8").trim() + "\nreplacement-end", "info"),
  });
  function record(value) {
    const path = process.env.FIN_REPLACEMENT_LOG;
    if (path) appendFileSync(path, JSON.stringify(value) + "\n");
  }
  pi.on("session_before_switch", (event) => {
    record(["before", event.reason]);
    if (process.env.FIN_REPLACEMENT_CANCEL === "1") return { cancel: true };
  });
  pi.on("session_before_fork", (event) => record(["before", "fork", event.position]));
  pi.on("session_shutdown", (event) => {
    if (event.reason !== "quit") record(["shutdown", event.reason]);
  });
  const resultCount = (path) => {
    try {
      return readFileSync(path, "utf8").split("\n").filter((line) => line.startsWith('["result"')).length;
    } catch {
      return 0;
    }
  };
  let settleTimer;
  pi.on("session_shutdown", () => clearInterval(settleTimer));
  pi.on("session_start", (event, ctx) => {
    if (event.reason !== "startup") {
      const roles = ctx.sessionManager.getBranch().filter((e) => e.type === "message").map((e) => e.message.role);
      record(["start", event.reason, !!event.previousSessionFile, roles]);
      if (ctx.mode === "tui") {
        ctx.ui.notify(`replacement-ready:${event.reason}:${roles.length}`, "info");
        // The replacing command resumes after the mode filled the editor (Pi fills the fork prefill once runtimeHost.fork resolves), and its own ctx is stale by then in Pi. The replacement's ctx announces the resume, so a driver can send the next key only after the prefill landed.
        const path = process.env.FIN_REPLACEMENT_LOG;
        const before = resultCount(path);
        settleTimer = setInterval(() => {
          if (resultCount(path) <= before) return;
          clearInterval(settleTimer);
          ctx.ui.notify(`replacement-settled:${event.reason}:${roles.length}`, "info");
        }, 10);
        settleTimer.unref();
      }
    }
  });
  pi.registerCommand("replace-probe", {
    handler: async (args, ctx) => {
      const [operation, relativePath] = args.split(" ");
      const path = resolve(ctx.cwd, relativePath);
      process.env.FIN_REPLACEMENT_LOG = path;
      let result;
      if (operation === "resume") {
        writeFileSync(path, "");
        const session = path + ".jsonl";
        const base = { timestamp: "2026-01-01T00:00:00.000Z" };
        const entries = [
          { ...base, type: "session", version: 3, id: randomUUID(), cwd: ctx.cwd },
          { ...base, type: "message", id: "u1", parentId: null, message: { role: "user", content: "first", timestamp: 1 } },
          { ...base, type: "message", id: "a1", parentId: "u1", message: { role: "assistant", content: [{ type: "text", text: "reply" }], api: "openai-completions", provider: "test-faux", model: "faux-1", stopReason: "stop", timestamp: 2,
            usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } } },
          { ...base, type: "message", id: "u2", parentId: "a1", message: { role: "user", content: "second", timestamp: 3 } },
        ];
        writeFileSync(session, entries.map((e) => JSON.stringify(e)).join("\n") + "\n");
        result = await ctx.switchSession(session);
      } else if (operation === "invalid") {
        result = await ctx.fork("missing-entry");
      } else if (operation === "at") {
        result = await ctx.fork("a1", { position: "at" });
      } else if (operation === "before") {
        result = await ctx.fork("u1");
      } else if (operation === "cancel") {
        process.env.FIN_REPLACEMENT_CANCEL = "1";
        result = await ctx.newSession();
        delete process.env.FIN_REPLACEMENT_CANCEL;
      } else if (operation === "new") {
        result = await ctx.newSession({ parentSession: path + ".jsonl" });
      } else {
        throw new Error("unknown probe operation");
      }
      record(["result", operation, result.cancelled]);
      if (operation === "cancel" && ctx.mode === "tui") ctx.ui.notify("replacement-cancelled:" + result.cancelled, "info");
    },
  });
}
