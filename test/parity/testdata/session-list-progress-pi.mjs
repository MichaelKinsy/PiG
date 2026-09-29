import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const { SessionManager } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/session-manager.js")));
const dir = mkdtempSync(join(tmpdir(), "pi-session-list-progress-"));
try {
  for (const label of ["A", "B"]) {
    const cwd = join(dir, `project-${label.toLowerCase()}`);
    mkdirSync(cwd);
    const session = SessionManager.create(cwd, dir);
    session.appendMessage({ role: "user", content: `from ${label}`, timestamp: Date.now() });
    session.appendMessage({ role: "assistant", content: [{ type: "text", text: `reply to from ${label}` }], api: "anthropic-messages", provider: "anthropic", model: "test", usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: Date.now() });
  }
  const controller = new AbortController();
  let callbacks = 0;
  await assert.rejects(SessionManager.listAll(dir, (_loaded, _total, partial) => { callbacks++; if (partial !== undefined) controller.abort(); }, controller.signal), { name: "AbortError" });
  await assert.rejects(SessionManager.listAll(undefined, controller.signal), { name: "AbortError" });
  assert.equal(callbacks, 1);
  console.log(`SESSION_LIST cancelled=${controller.signal.aborted} callbacks=${callbacks}`);
} finally { rmSync(dir, { recursive: true, force: true }); }
