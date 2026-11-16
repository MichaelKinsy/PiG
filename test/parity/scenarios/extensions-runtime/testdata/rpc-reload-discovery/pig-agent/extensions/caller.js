// The extension that stays. Its command changes the extensions folder as a user does before a reload (one sibling's file removed, another extension's file
// added) and awaits ctx.reload(); a second command reports the extension commands that are loaded.
import { unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

const added = `export default function (pi) {
  pi.registerCommand("added-hello", { description: "Added by the command", handler: async () => {} });
}
`;

export default function (pi) {
  pi.registerCommand("change-and-reload", {
    description: "Remove one sibling, add another, reload",
    handler: async (_args, ctx) => {
      const extensions = join(getAgentDir(), "extensions");
      unlinkSync(join(extensions, "sibling.js"));
      writeFileSync(join(extensions, "zz-added.js"), added);
      await ctx.reload();
    },
  });
  pi.registerCommand("loaded", {
    description: "Report the extension commands that are loaded",
    handler: async (_args, ctx) => {
      const names = pi.getCommands().filter((command) => command.sourceInfo.path.startsWith(join(getAgentDir(), "extensions"))).map((command) => command.name).sort();
      ctx.ui.notify(`loaded: ${names.join(",")}`, "info");
    },
  });
}
