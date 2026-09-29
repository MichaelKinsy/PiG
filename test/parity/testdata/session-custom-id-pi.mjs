import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const { SessionManager } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/session-manager.js")));
for (const id of ["", "-abc", "abc-", "_abc", "abc_", ".abc", "abc.", "abc/def", "abc\\def", "abc def"]) {
  assert.throws(() => SessionManager.inMemory("/project", { id }), /Session id must be non-empty, contain only alphanumeric characters/);
}
assert.equal(SessionManager.inMemory("/project", { id: "abc-123_def.456" }).getSessionId(), "abc-123_def.456");
const dir = mkdtempSync(join(tmpdir(), "pi-custom-session-id-"));
try {
  const source = join(dir, "source.jsonl");
  writeFileSync(source, JSON.stringify({ type: "session", version: 3, id: "source-session-id", timestamp: "2026-01-01T00:00:00Z", cwd: "/project" }) + "\n");
  const fork = SessionManager.forkFrom(source, dir, dir, { id: "forked-session-id" });
  assert.equal(fork.getSessionId(), "forked-session-id");
  assert.equal(fork.getHeader().id, fork.getSessionId());
  assert.equal(fork.getHeader().parentSession, source);
  assert.match(basename(fork.getSessionFile()), /^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z_forked-session-id\.jsonl$/);
  console.log("SESSION_ID custom=forked-session-id parent=true filename=true");
} finally { rmSync(dir, { recursive: true, force: true }); }
