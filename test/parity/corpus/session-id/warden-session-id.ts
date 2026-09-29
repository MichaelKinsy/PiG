import assert from "node:assert/strict";
import { writeFileSync } from "node:fs";
import warden from "pi-warden/extension";

// Capture only the package's tool definition; its factory, handlers, context and
// loop store remain unchanged. The command needs no model or network request.
export default async function (pi) {
  let loops;
  await warden({ ...pi, registerTool(definition) {
    if (definition.name === "warden_loops") loops = definition;
    pi.registerTool(definition);
  } });
  assert.ok(loops, "pi-warden did not register warden_loops");
  pi.registerCommand("warden-session-id", {
    description: "Exercise the pinned pi-warden per-session loop store",
    async handler(_args, ctx) {
      const added = await loops.execute("add", { action: "add", text: "trial check" }, ctx.signal, undefined, ctx);
      const listed = await loops.execute("list", { action: "list" }, ctx.signal, undefined, ctx);
      writeFileSync(process.env.SESSION_ID_REPORT, JSON.stringify({ added, listed }) + "\n");
    },
  });
}
