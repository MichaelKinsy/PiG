// The extension that stays: its command removes the provider extension's file, as a user does by deleting it before running /reload.
import { unlinkSync } from "node:fs";
import { join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.registerCommand("removed-unlink", {
    description: "Remove the provider extension's file",
    handler: async (_args, ctx) => {
      unlinkSync(join(getAgentDir(), "extensions", "removed-provider.js"));
      ctx.ui.notify("removed-unlinked", "info");
    },
  });
}
