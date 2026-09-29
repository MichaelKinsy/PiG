import fs from "node:fs";

// Persist across Pi's fresh extension factories; validate identities rather than
// masking random Session filenames in the comparator.
export default function (pi) {
  const path = process.env.LIFECYCLE_LOG;
  const load = () => fs.existsSync(path) ? JSON.parse(fs.readFileSync(path, "utf8")) : { trace: [] };
  const save = (state) => fs.writeFileSync(path, JSON.stringify(state));
  for (const type of ["session_before_switch", "session_before_fork"]) {
    pi.on(type, (event, ctx) => {
      const state = load();
      state.old = ctx.sessionManager.getSessionFile();
      state.trace.push(`before:${event.reason ?? event.position}`);
      save(state);
    });
  }
  pi.on("session_shutdown", (event, ctx) => {
    if (event.reason === "quit") return;
    const state = load();
    state.target = event.targetSessionFile;
    state.trace.push(`${event.reason}:shutdown:${state.target && state.old === ctx.sessionManager.getSessionFile() ? "ok" : "BAD"}`);
    save(state);
  });
  pi.on("session_start", (event, ctx) => {
    if (event.reason === "startup") return;
    const state = load();
    const valid = event.previousSessionFile && event.previousSessionFile === state.old && state.target === ctx.sessionManager.getSessionFile();
    state.trace.push(`${event.reason}:start:${valid ? "ok" : "BAD"}`);
    save(state);
    ctx.ui.notify(`lifecycle ready ${event.reason}`, "info");
  });
  pi.registerCommand("lifecycle-status", {
    handler: async (_args, ctx) => ctx.ui.notify("LIFECYCLE " + load().trace.join(" "), "info"),
  });
}
