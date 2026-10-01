import assert from "node:assert/strict";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const { SessionManager } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/session-manager.js")));
const user = content => ({ role: "user", content, timestamp: 1 });
const assistant = text => ({ role: "assistant", content: [{ type: "text", text }], api: "anthropic-messages", provider: "anthropic", model: "test", usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: 2 });
{
  // Pi regression 8989: a label may be the first kept compaction entry.
  const s = SessionManager.inMemory("/project");
  const old = s.appendMessage(user("old"));
  const label = s.appendLabelChange(old, "checkpoint");
  const kept = s.appendMessage(user("kept"));
  const comp = s.appendCompaction("summary", label, 100);
  const leaf = s.appendMessage(user("after"));
  s.createBranchedSession(leaf);
  assert.equal(s.getEntry(comp).firstKeptEntryId, kept);
  const messages = s.buildSessionContext().messages;
  assert.equal(messages.length, 3);
  assert.equal(messages[0].role, "compactionSummary");
  assert.equal(messages[0].summary, "summary");
  assert.deepEqual(messages.slice(1).map(m => m.content), ["kept", "after"]);
  console.log("SESSION_FORK boundary=kept context=summary,kept,after");
}
{
  const s = SessionManager.inMemory("/project");
  const a = s.appendMessage(user("hello"));
  s.appendLabelChange(a, "checkpoint");
  const model = s.appendModelChange("anthropic", "claude-test");
  const b = s.appendMessage(assistant("hi"));
  s.appendLabelChange(a, "important");
  s.appendLabelChange(b, "also-important");
  const ta = s.getTree()[0].labelTimestamp;
  assert.equal(s.createBranchedSession(b), undefined);
  assert.equal(s.getEntry(model).parentId, a);
  assert.equal(s.getLabel(a), "important");
  assert.equal(s.getLabel(b), "also-important");
  assert.equal(s.getTree()[0].labelTimestamp, ta);
  assert.equal(s.getEntries().filter(e => e.type === "label").length, 2);
  s.appendLabelChange(a, "");
  assert.equal(s.getTree()[0].labelTimestamp, undefined);
  assert.throws(() => s.appendLabelChange("non-existent", "label"), { message: "Entry non-existent not found" });
  assert.throws(() => s.branch("nonexistent"), { message: "Entry nonexistent not found" });
}
// tree-traversal.test.ts:483-585: a fork is written once its path holds a user or assistant message (session-manager.ts:1166-1170, 1717-1724), not only an assistant.
const roles = path => readFileSync(path, "utf8").trim().split("\n").map(line => JSON.parse(line)).map(e => e.type === "message" ? e.message.role : e.type === "custom" ? "custom" : e.type);
const dir = mkdtempSync(join(tmpdir(), "pi-session-fork-"));
try {
  {
    const s = SessionManager.create(dir, dir);
    const setup = s.appendModelChange("anthropic", "claude-sonnet-4-5");
    s.appendMessage(user("first question"));
    s.appendMessage(assistant("first answer"));
    const path = s.createBranchedSession(setup);
    assert.notEqual(path, undefined);
    assert.equal(existsSync(path), false);
    s.appendMessage(user("new question"));
    assert.equal(existsSync(path), true);
    s.appendCustomEntry("preset-state", { name: "plan" });
    s.appendMessage(assistant("new answer"));
    assert.deepEqual(roles(path), ["session", "model_change", "user", "custom", "assistant"]);
  }
  {
    const s = SessionManager.create(dir, dir);
    const root = s.appendMessage(user("first question"));
    s.appendMessage(assistant("first answer"));
    const path = s.createBranchedSession(root);
    assert.equal(existsSync(path), true);
    s.appendMessage(assistant("new answer"));
    assert.deepEqual(roles(path), ["session", "user", "assistant"]);
  }
} finally { rmSync(dir, { recursive: true, force: true }); }
