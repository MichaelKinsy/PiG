import assert from "node:assert/strict";
import { appendFileSync, existsSync } from "node:fs";

// Pi 0.87.1 session-manager.ts:1057-1087 gives persisted and in-memory sessions
// the same UUIDv7/header identity. print-mode.ts binds it before session_start.
export default function (pi) {
  let initialId;
  const record = (event, ctx) => {
    const session = ctx.sessionManager;
    const id = session.getSessionId();
    assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    assert.equal(session.getHeader().id, id);
    if (event === "session_start") initialId = id;
    assert.equal(id, initialId);
    const file = session.getSessionFile();
    const persisted = session.isPersisted();
    assert.equal(Boolean(file), persisted);
    const row = { event, uuidv7: true, headerMatches: true, stable: true, persisted, fileAssigned: Boolean(file), fileExists: Boolean(file && existsSync(file)) };
    appendFileSync(process.env.SESSION_ID_REPORT, JSON.stringify(row) + "\n");
    if (process.env.SESSION_ID_RAW) {
      appendFileSync(process.env.SESSION_ID_RAW, JSON.stringify({ ...row, id, header: session.getHeader(), file: file ?? null }) + "\n");
    }
  };
  for (const event of ["session_start", "agent_end", "session_shutdown"]) {
    pi.on(event, (_event, ctx) => record(event, ctx));
  }
  pi.registerTool({
    name: "echo_bridge",
    label: "Session identity probe",
    description: "Check the extension-visible session identity and echo text.",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    async execute(_id, args, _signal, _update, ctx) {
      record("tool", ctx);
      return { content: [{ type: "text", text: `echo-bridge: ${args.text}` }] };
    },
  });
}
