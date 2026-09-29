import { readFileSync } from "node:fs";
import { join } from "node:path";

// Read the real command context and persisted settings after /login; do not select a model here.
export default function (pi: any) {
  pi.registerCommand("post-login-state", {
    description: "Report post-login model and persisted default",
    handler: async (_args: string, ctx: any) => {
      const agentDir = process.env.PIG_CODING_AGENT_DIR || process.env.PI_CODING_AGENT_DIR!;
      const settings = JSON.parse(readFileSync(join(agentDir, "settings.json"), "utf8"));
      ctx.ui.notify(`post-login-state: current=${ctx.model?.provider}/${ctx.model?.id} default=${settings.defaultProvider}/${settings.defaultModel}`, "info");
    },
  });
}
