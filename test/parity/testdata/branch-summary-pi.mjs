import assert from "node:assert/strict";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const fromPackage = path => import(pathToFileURL(resolve("extensions/sdk-ts/node_modules", path)));
const { generateBranchSummary } = await fromPackage("@earendil-works/pi-coding-agent/dist/core/compaction/index.js");
const { createAssistantMessageEventStream, fauxAssistantMessage } = await import(import.meta.resolve("@earendil-works/pi-ai", pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/package.json")).href));
const model = { id: "test-model", name: "Test Model", api: "anthropic-messages", provider: "anthropic", baseUrl: "https://api.anthropic.com", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 8192 };
const entries = [{ type: "message", id: "branch-user", parentId: null, timestamp: new Date(1).toISOString(), message: { role: "user", content: "Abandoned request", timestamp: 1 } }];
// branch-summarization.test.ts at Pi 8efd314f: three cases; Pi 6fcb4867 (#9512) sets no maxTokens on a branch summary.
const cases = [
  { maxTokens: 8192, content: [{ type: "text", text: "summary" }], reason: "stop", error: "" },
  { maxTokens: 8192, content: [{ type: "toolCall", id: "tool-call-1", name: "read", arguments: { path: "README.md" } }], reason: "toolUse", error: "Branch summarization attempted to call a tool" },
  { maxTokens: 8192, content: [{ type: "text", text: "partial" }], reason: "length", error: "Branch summarization failed: generation hit the token cap and the summary is incomplete" },
];
for (const [i, tc] of cases.entries()) {
  let options;
  const result = await generateBranchSummary(entries, {
    model: { ...model, maxTokens: tc.maxTokens }, signal: new AbortController().signal,
    streamFn: (_model, _context, opts) => {
      options = opts;
      const stream = createAssistantMessageEventStream();
      queueMicrotask(() => stream.push({ type: "done", reason: tc.reason, message: { ...fauxAssistantMessage(""), content: tc.content, api: model.api, provider: model.provider, model: model.id, stopReason: tc.reason } }));
      return stream;
    },
  });
  assert.equal(options.maxTokens, undefined);
  assert.equal(options.toolChoice, undefined);
  assert.equal(result.error ?? "", tc.error);
  console.log(`BRANCH_SUMMARY ${i} max=${options.maxTokens ?? 0} error=${JSON.stringify(result.error ?? "")}`);
}
