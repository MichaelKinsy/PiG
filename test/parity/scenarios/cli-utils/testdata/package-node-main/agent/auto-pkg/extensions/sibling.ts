import { writeFileSync } from "node:fs";

export default function (pi) {
  pi.registerCommand("discovery-sibling", {
    handler: async (args) => {
      const names = pi.getCommands().map((command) => command.name).filter((name) => name.startsWith("discovery-"));
      writeFileSync(args.trim(), JSON.stringify(names) + "\n");
    },
  });
}
