import assert from "node:assert/strict";
import { createHarness, fauxAssistantMessage } from "./pi-session-harness.mjs";
const h = await createHarness();
let started, release;
const responseStarted = new Promise(resolve => { started = resolve; });
const responseReleased = new Promise(resolve => { release = resolve; });
try {
  h.setResponses([async () => { started(); await responseReleased; return fauxAssistantMessage("reply"); }]);
  const prompt = h.session.prompt("wait for abort");
  await responseStarted;
  h.session.recordBashResult("echo buffered", { output: "buffered", exitCode: 0, cancelled: false, truncated: false });
  assert.equal(h.sessionManager.getEntries().some(e => e.type === "message" && e.message.role === "bashExecution"), false);
  const aborted = h.session.abort();
  release();
  await Promise.all([prompt, aborted]);
  assert.equal(h.sessionManager.getEntries().at(-1).message.role, "bashExecution");
  assert.equal(h.session.messages.at(-1).role, "bashExecution");
  console.log(`BASH_ORDER tail=${h.session.messages.at(-1).role}`);
} finally { release(); h.cleanup(); }
