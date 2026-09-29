import { writeFileSync } from "node:fs";

export default function (pi) {
  pi.registerCommand("ancestor-info", {
    description: "Record skill order, collision winners, and trust provenance",
    handler: async (args) => {
      const skills = pi.getCommands().filter((command) => command.source === "skill").map((command) => ({
        name: command.name,
        description: command.description,
        source: command.sourceInfo.source,
        scope: command.sourceInfo.scope,
      }));
      writeFileSync(args.trim(), JSON.stringify(skills) + "\n");
    },
  });
}
