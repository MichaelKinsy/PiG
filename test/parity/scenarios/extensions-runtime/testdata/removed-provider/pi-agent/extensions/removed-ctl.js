// The extension that stays: it removes the provider extension's file and reloads, as a user does by deleting the file and running /reload, and
// announces each reload once the new instance started (the old instance's pi and ctx are stale after the reload).
import { unlinkSync } from "node:fs";
import { join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.on("session_start", async (event, ctx) => {
    if (event.reason !== "reload") return;
    globalThis.__removedReloads = (globalThis.__removedReloads ?? 0) + 1;
    ctx.ui.notify(`removed-reload-done-${globalThis.__removedReloads}`, "info");
  });
  pi.registerCommand("removed-remove-and-reload", {
    description: "Remove the provider extension's file, then reload",
    handler: async (_args, ctx) => {
      unlinkSync(join(getAgentDir(), "extensions", "removed-provider.js"));
      await ctx.reload();
    },
  });
  pi.registerCommand("removed-reload", {
    description: "Reload",
    handler: async (_args, ctx) => {
      await ctx.reload();
    },
  });
}
