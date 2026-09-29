import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

// Like gentle-pi's session startup, a later extension creates a repository after the footer binds its git metadata.
export default function (pi) {
  pi.registerCommand("refresh-footer-state", {
    handler: (_args, ctx) => {
      ctx.ui.setStatus("passive", "refreshed");
      ctx.ui.notify("SCROLL FOOTER RESULT FINAL", "info");
    },
  });
  pi.on("session_start", (_event, ctx) => {
    mkdirSync(join(ctx.cwd, ".git"));
    writeFileSync(join(ctx.cwd, ".git", "HEAD"), "ref: refs/heads/late\n");
    ctx.ui.setStatus("passive", "ready");
  });
}
