import assert from "node:assert/strict";
import type { ExtensionAPI, SessionBeforeCompactEvent, SessionCompactEvent } from "@earendil-works/pi-coding-agent";

// packages/coding-agent/test/compaction-extensions-example.test.ts:16
const exampleExtension = (pi: ExtensionAPI) => {
  pi.on("session_before_compact", async (event: SessionBeforeCompactEvent, ctx) => {
    const { preparation, branchEntries } = event;
    const { sessionManager, modelRegistry } = ctx;
    const { messagesToSummarize, turnPrefixMessages, tokensBefore, firstKeptEntryId, isSplitTurn } = preparation;
    assert.equal(Array.isArray(messagesToSummarize), true);
    assert.equal(Array.isArray(turnPrefixMessages), true);
    assert.equal(typeof isSplitTurn, "boolean");
    assert.equal(typeof tokensBefore, "number");
    assert.equal(typeof sessionManager.getEntries, "function");
    assert.equal(typeof modelRegistry.getApiKeyAndHeaders, "function");
    assert.equal(typeof firstKeptEntryId, "string");
    assert.equal(Array.isArray(branchEntries), true);
    const summary = messagesToSummarize
      .filter((m) => m.role === "user")
      .map((m) => `- ${typeof m.content === "string" ? m.content.slice(0, 100) : "[complex]"}`)
      .join("\n");
    return { compaction: { summary: `User requests:\n${summary}`, firstKeptEntryId, tokensBefore } };
  });
};

// packages/coding-agent/test/compaction-extensions-example.test.ts:136
const checkCompactEvent = (pi: ExtensionAPI) => {
  pi.on("session_compact", async (event: SessionCompactEvent) => {
    const entry = event.compactionEntry;
    const fromExtension = event.fromExtension;
    assert.equal(entry.type, "compaction");
    assert.equal(typeof entry.summary, "string");
    assert.equal(typeof entry.tokensBefore, "number");
    assert.equal(typeof fromExtension, "boolean");
  });
};

export default function (pi: ExtensionAPI) {
  assert.equal(typeof exampleExtension, "function");
  assert.equal(typeof checkCompactEvent, "function");
  exampleExtension(pi);
  checkCompactEvent(pi);
}
