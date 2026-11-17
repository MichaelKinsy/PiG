// /replace-after-settings-change adds a model scope pattern that matches nothing to the agent directory's settings and then replaces the session twice through ctx.newSession():
// the runtime factory runs again and returns a diagnostic that did not exist at startup (Pi c47ad0432bb8, #12115).
import { writeFileSync } from "node:fs";
import { join } from "node:path";

export default function (pi) {
  pi.registerCommand("replace-after-settings-change", {
    description: "Add an unmatched enabledModels pattern and replace the session twice",
    handler: async (_args, ctx) => {
      const agentDir = process.env.PIG_CODING_AGENT_DIR ?? process.env.PI_CODING_AGENT_DIR;
      writeFileSync(join(agentDir, "settings.json"), JSON.stringify({ enabledModels: ["test-faux/faux-1", "github-copilot/missing-model"] }));
      await ctx.newSession();
      await ctx.newSession();
    },
  });
}
