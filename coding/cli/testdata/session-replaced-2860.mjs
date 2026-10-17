// session-replaced-2860.mjs runs the bodies of Pi's 2860-replaced-session-context.test.ts cases as extension commands. Each line goes to the file named by PIG_TEST_2860_LOG; process ids name the extension instances, because each Session's extensions run in their own process here.
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";

const log = (line) => appendFileSync(process.env.PIG_TEST_2860_LOG, `${line}\n`);
const text = (message) => typeof message.content === "string"
  ? message.content
  : message.content.filter((part) => part.type === "text").map((part) => part.text).join("");
const conversation = (sessionManager) => sessionManager.getBranch()
  .filter((entry) => entry.type === "message" && entry.message.role !== "system")
  .map((entry) => `${entry.message.role}:${text(entry.message)}`)
  .join("|");
const marks = () => {
  try { return JSON.parse(readFileSync(process.env.PIG_TEST_2860_MARKS, "utf8")); } catch { return {}; }
};

export default function replacedSession2860(pi) {
  pi.on("session_start", () => log(`start:${process.pid}`));
  pi.on("session_shutdown", () => log(`shutdown:${process.pid}`));

  pi.registerCommand("repro", {
    description: "repro",
    handler: async (_args, ctx) => {
      const oldCtx = ctx;
      const oldPi = pi;
      const oldSessionFile = ctx.sessionManager.getSessionFile();
      await ctx.newSession({
        parentSession: oldSessionFile,
        withSession: async (replacedCtx) => {
          log(`with:${process.pid}`);
          const replacementSessionFile = replacedCtx.sessionManager.getSessionFile();
          log(`replacement:${replacementSessionFile !== undefined && replacementSessionFile !== oldSessionFile}`);
          try {
            oldCtx.sessionManager.getSessionFile();
            log("staleCtx:false");
          } catch {
            log("staleCtx:true");
          }
          try {
            oldPi.sendUserMessage("stale message");
            log("stalePi:false");
          } catch {
            log("stalePi:true");
          }
          await replacedCtx.sendUserMessage("reply with exactly: hello reply");
          log(`idle:${replacedCtx.isIdle()}`);
          log(`model:${replacedCtx.model?.id}`);
          log(`conversation:${conversation(replacedCtx.sessionManager)}`);
        },
      });
    },
  });

  // types.ts:411, agent-session-runtime.ts:254-257: setup seeds the replacement Session through its SessionManager before withSession runs.
  pi.registerCommand("seed-it", {
    description: "seed-it",
    handler: async (_args, ctx) => {
      await ctx.newSession({
        setup: async (sessionManager) => {
          const ids = [sessionManager.appendCustomMessageEntry("seed-msg", "from setup", true), sessionManager.appendSessionInfo("seeded-session")];
          log(`setup:${ids.every((id) => typeof id === "string" && id.length > 0) && new Set(ids).size === ids.length}`);
        },
        withSession: async (replacedCtx) => {
          log(`seedName:${replacedCtx.sessionManager.getSessionName()}`);
          log(`seedEntry:${replacedCtx.sessionManager.getEntries().filter((entry) => entry.type === "custom_message").map((entry) => `${entry.customType}=${entry.content}`).join("|")}`);
        },
      });
    },
  });

  // agent-session-runtime.ts:187-194 awaits the callback, so its rejection rejects the replacement call with the same error.
  pi.registerCommand("throw-it", {
    description: "throw-it",
    handler: async (_args, ctx) => {
      const thrown = new Error("callback failed");
      try {
        await ctx.newSession({ withSession: async () => { throw thrown; } });
        log("caught:none");
      } catch (error) {
        log(error === thrown ? `caught:${error.message}` : `caught:other:${error?.message}`);
      }
    },
  });

  // A context kept past its callback must not block the extension: the command still returns.
  pi.registerCommand("keep-it", {
    description: "keep-it",
    handler: async (_args, ctx) => {
      let kept;
      await ctx.newSession({ withSession: async (replacedCtx) => { kept = replacedCtx; } });
      try { kept.sessionManager.getSessionFile(); } catch {}
      try { kept.isIdle(); } catch {}
      try { await kept.sendUserMessage("kept message"); } catch {}
      log("kept:returned");
    },
  });

  pi.registerCommand("fork-it", {
    description: "fork-it",
    handler: async (_args, ctx) => {
      const leafId = ctx.sessionManager.getLeafId();
      if (!leafId) throw new Error("Missing leaf id");
      await ctx.fork(leafId, {
        position: "at",
        withSession: async (replacedCtx) => {
          await replacedCtx.sendUserMessage("reply with exactly: fork reply");
          log(`conversation:${conversation(replacedCtx.sessionManager)}`);
        },
      });
    },
  });

  pi.registerCommand("mark", {
    description: "mark",
    handler: async (name, ctx) => {
      writeFileSync(process.env.PIG_TEST_2860_MARKS, JSON.stringify({ ...marks(), [name]: ctx.sessionManager.getSessionFile() }));
    },
  });

  pi.registerCommand("new", {
    description: "new",
    handler: async (_args, ctx) => {
      await ctx.newSession();
    },
  });

  pi.registerCommand("switch", {
    description: "switch",
    handler: async (name, ctx) => {
      await ctx.switchSession(marks()[name]);
    },
  });

  pi.registerCommand("switch-it", {
    description: "switch-it",
    handler: async (_args, ctx) => {
      const target = marks().target;
      await ctx.switchSession(target, {
        withSession: async (replacedCtx) => {
          await replacedCtx.sendUserMessage("reply with exactly: switch reply");
          log(`switched:${replacedCtx.sessionManager.getSessionFile() === target}`);
          log(`conversation:${conversation(replacedCtx.sessionManager)}`);
        },
      });
    },
  });
}
