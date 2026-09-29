// Pi owns independent SDK composition. This adapter only binds its lifetime to
// the extension connection; it never prompts or replaces the main Session.
export * from "./pi-dist/pi-coding-agent/sdk-bundle/sdk.js";
import { createAgentSession as createPiAgentSession } from "./pi-dist/pi-coding-agent/sdk-bundle/sdk.js";
import { currentRuntime } from "../state.mjs";
import { independentSessionOwner } from "../independent-session-owner.mjs";

export async function createAgentSession(options) {
  const runtime = currentRuntime();
  if (!runtime) return createPiAgentSession(options);
  const owner = independentSessionOwner(runtime);
  if (owner.closed) throw new Error("Extension connection is closed");
  const creation = createPiAgentSession(options).then(async result => {
    const { session } = result;
    if (owner.closed) {
      try { await session.abort(); } finally { session.dispose(); }
      throw new Error("Extension connection closed while creating a Session");
    }
    owner.sessions.add(session);
    const dispose = session.dispose;
    session.dispose = function () {
      try { return dispose.call(this); } finally { owner.sessions.delete(this); }
    };
    return result;
  });
  owner.pending.add(creation);
  try { return await creation; } finally { owner.pending.delete(creation); }
}
