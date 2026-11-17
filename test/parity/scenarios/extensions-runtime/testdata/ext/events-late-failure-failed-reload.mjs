// A pi.events handler that is still running when a /reload fails and fails afterwards. Pi invalidates the old runner right after session_shutdown
// (agent-session.ts:3825-3826) and a reload that throws leaves the session on that stale runner; runtime.reportError (18e99051be24) calls emitError()
// only `if (!this.staleMessage)`, so the late failure is dropped: nothing is shown for it, and the session goes on.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.events.on("demo:late", async () => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    throw new Error("late handler failed");
  });
  pi.registerTool({
    name: "late_probe",
    label: "late_probe",
    description: "a tool that answers after the late failure",
    parameters: { type: "object", properties: {} },
    execute: async () => ({ content: [{ type: "text", text: "late probe done" }] }),
  });
  pi.registerCommand("late-then-reload-fails", {
    description: "Emit demo:late, then reload with a settings entry the resource loader rejects",
    handler: async (_args, ctx) => {
      pi.events.emit("demo:late", {});
      const path = join(getAgentDir(), "settings.json");
      const original = readFileSync(path, "utf8");
      writeFileSync(path, JSON.stringify({ ...JSON.parse(original), prompts: ["file:///a%2Fb"] }));
      try {
        await ctx.reload();
      } catch {
        // The interactive mode shows the failure; the settings go back so only the reload's own failure is in the way.
      }
      writeFileSync(path, original);
    },
  });
}
