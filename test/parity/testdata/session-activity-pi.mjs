import assert from "node:assert/strict";
import { mkdtempSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

// Uses the unchanged, installed Pi 0.87.1 SessionManager.
const { SessionManager } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/session-manager.js")));
const dir = mkdtempSync(join(tmpdir(), "pi-activity-"));
const assistant = (text, timestamp) => ({ role: "assistant", content: [{ type: "text", text }], api: "openai-completions", provider: "openai", model: "test", usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp });
try {
  // .upstream/v0.87.1/packages/coding-agent/test/session-info-modified-timestamp.test.ts:49
  const path = join(dir, "session.jsonl");
  writeFileSync(path, JSON.stringify({ type: "session", id: "test-session", version: 3, timestamp: new Date(0).toISOString(), cwd: "/tmp" }) + "\n");
  const s = SessionManager.open(path);
  s.appendMessage(assistant("hi", 1000));
  utimesSync(path, new Date(2000), new Date(2000));
  s.appendMessage(assistant("later", 3000));
  const [info] = await SessionManager.list("/tmp", dir);
  assert.equal(info.modified.getTime(), 3000);
  console.log(`SESSION_ACTIVITY modified=${info.modified.getTime()}`);
  rmSync(path);
  // JSON whitespace must not change discovery or activity.
  const spaced = join(dir, "spaced.jsonl");
  writeFileSync(spaced, JSON.stringify({ type: "session", version: 3, id: "spaced", cwd: "/tmp", timestamp: "2025-01-01T00:00:00Z" }) + "\n" + JSON.stringify({ type: "message", id: "msg", parentId: null, timestamp: "2025-01-02T00:00:00Z", message: { role: "user", content: "hi", timestamp: 3000 } }).replaceAll(":", ": ") + "\n");
  const [spacedInfo] = await SessionManager.list("/tmp", dir);
  assert.equal(spacedInfo.modified.getTime(), 3000);
  assert.equal(spacedInfo.firstMessage, "hi");
  rmSync(spaced);
  const paths = [];
  for (const [i, id] of ["old", "new"].entries()) {
    const session = SessionManager.create("/project", dir, { id });
    session.appendMessage(assistant(id, 2000 - i * 1000));
    utimesSync(session.getSessionFile(), new Date((i + 10) * 1000), new Date((i + 10) * 1000));
    paths.push(session.getSessionFile());
  }
  assert.deepEqual((await SessionManager.list("/project", dir)).map(s => s.id), ["old", "new"]);
  assert.equal(SessionManager.continueRecent("/project", dir).getSessionId(), "new");
  console.log("SESSION_ACTIVITY order=old,new continue=new");
} finally { rmSync(dir, { recursive: true, force: true }); }
