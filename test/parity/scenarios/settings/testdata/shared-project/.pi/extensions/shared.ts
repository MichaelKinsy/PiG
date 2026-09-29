import { writeFileSync } from "node:fs";
import { CONFIG_DIR_NAME, getAgentDir, SettingsManager } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.registerCommand("shared-check", {
    description: "Inspect selected shared resources",
    handler: async (file, ctx) => {
      const settings = SettingsManager.create(ctx.cwd);
      writeFileSync(file, JSON.stringify({
        configDir: CONFIG_DIR_NAME,
        agent: getAgentDir() === process.env.PI_CODING_AGENT_DIR,
        theme: settings.getTheme(),
        system: ctx.getSystemPrompt().includes("SHARED-SYSTEM-INSTRUCTIONS"),
        commands: pi.getCommands().map(c => c.name).filter(n => n.includes("shared")).sort(),
      }));
    },
  });
}
