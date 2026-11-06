import { readFileSync } from "node:fs";
import { join } from "node:path";

// Reports whether settings.json already records lastChangelogVersion when session_start runs.
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    const agentDir = process.env.PI_CODING_AGENT_DIR ?? process.env.PIG_CODING_AGENT_DIR;
    let state = "settings.json missing";
    try {
      const settings = JSON.parse(readFileSync(join(agentDir, "settings.json"), "utf8"));
      state = settings.lastChangelogVersion ? "recorded" : "not recorded";
    } catch {}
    ctx.ui.notify(`STARTUP CHANGELOG VERSION: ${state}`, "info");
  });
}
