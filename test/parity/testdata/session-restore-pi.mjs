import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const { SessionManager } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/session-manager.js")));
const dir = mkdtempSync(join(tmpdir(), "pi-restored-migrations-"));
try {
  const path = join(dir, "v1.jsonl");
  const entries = [
    { type: "session", id: "sess-1", timestamp: "2025-01-01T00:00:00Z", cwd: "/tmp" },
    { type: "message", timestamp: "2025-01-01T00:00:01Z", message: { role: "user", content: "hi", timestamp: 1 } },
    { type: "message", timestamp: "2025-01-01T00:00:02Z", message: { role: "assistant", content: [{ type: "text", text: "hello" }], api: "test", provider: "test", model: "test", usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0 }, stopReason: "stop", timestamp: 2 } },
  ];
  writeFileSync(path, entries.map(e => JSON.stringify(e)).join("\n") + "\n");
  const s = SessionManager.open(path), restored = s.getEntries();
  assert.equal(s.getHeader().version, 3);
  assert.equal(restored.length, 2);
  assert.equal(restored[0].id.length, 8);
  assert.equal(restored[1].id.length, 8);
  assert.equal(restored[0].parentId, null);
  assert.equal(restored[1].parentId, restored[0].id);
  assert.deepEqual(SessionManager.open(path).getEntries(), restored);
  assert.equal(JSON.parse(readFileSync(path, "utf8").split("\n")[0]).version, 3);
  console.log(`SESSION_MIGRATION version=${s.getHeader().version} entries=${restored.length} chain=${restored[1].parentId === restored[0].id}`);
  const old = { type: "message", id: "abc12345", parentId: null, timestamp: "2026-01-01T00:00:01Z", message: { role: "hookMessage", content: "from a hook", timestamp: 1 } };
  const memory = SessionManager.inMemory("/project", undefined, [{ type: "session", version: 2, id: "v2-session", timestamp: "2026-01-01T00:00:00Z", cwd: "/project" }, structuredClone(old)]);
  assert.equal(memory.getHeader().version, 3);
  assert.equal(memory.getEntries()[0].id, "abc12345");
  assert.equal(memory.getEntries()[0].message.role, "custom");
  assert.equal(SessionManager.inMemory("/project", undefined, [old]).getEntries()[0].message.role, "hookMessage");
} finally { rmSync(dir, { recursive: true, force: true }); }
