import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

export default function (pi) {
  pi.registerFlag("order-root", { type: "string" });
  pi.on("resources_discover", () => {
    const root = join(pi.getFlag("order-root"), "skills");
    for (const [dir, name] of [["01-first", "zulu"], ["02-second", "alpha"]]) {
      const path = join(root, dir);
      mkdirSync(path, { recursive: true });
      writeFileSync(join(path, "SKILL.md"), `---\nname: ${name}\ndescription: valid\n---\nbody`);
    }
    return { skillPaths: [root] };
  });
  pi.registerCommand("skill-order", {
    handler: async () => {
      const names = pi.getCommands().filter(command => command.source === "skill").map(command => command.name);
      writeFileSync(join(pi.getFlag("order-root"), "order.json"), JSON.stringify(names) + "\n");
    },
  });
}
