// The model context of a session: the path to the leaf, compaction and what it keeps, branch summaries, context edits, the thinking level and model along the path, an
// omitted, null, unknown and empty leaf, and an index passed in. Arguments are [entries, leafId, byIdPairs]; the call turns byIdPairs into the Map Pi takes.
const ts = "2026-01-02T03:04:05.678Z";
const base = (id, parentId, type) => ({ type, id, parentId, timestamp: ts });
const user = (id, parentId, text) => ({ ...base(id, parentId, "message"), message: { role: "user", content: text, timestamp: 1 } });
const assistant = (id, parentId, text, provider = "anthropic", model = "claude") => ({
  ...base(id, parentId, "message"),
  message: { role: "assistant", content: [{ type: "text", text }], provider, model, api: "x", usage: {}, stopReason: "stop", timestamp: 2 },
});
const system = (id, parentId, text) => ({ ...base(id, parentId, "message"), message: { role: "system", content: text, timestamp: 3 } });
const compaction = (id, parentId, firstKeptEntryId, extra = {}) => ({ ...base(id, parentId, "compaction"), summary: `summary ${id}`, firstKeptEntryId, tokensBefore: 100, ...extra });
const chain = [user("a", null, "one"), assistant("b", "a", "two"), user("c", "b", "three"), assistant("d", "c", "four", "openai", "gpt")];
const withSettings = [
  { ...base("t", null, "thinking_level_change"), thinkingLevel: "high" },
  { ...base("m", "t", "model_change"), provider: "google", modelId: "gemini" },
  user("a", "m", "hi"),
  assistant("b", "a", "hello", "anthropic", "claude"),
  { ...base("t2", "b", "thinking_level_change"), thinkingLevel: "low" },
];
const branched = [user("a", null, "root"), assistant("b", "a", "left"), user("x", "a", "right"), assistant("y", "x", "right again")];
export const cases = [
  { name: "empty", args: [[]] },
  { name: "chain, last leaf", args: [chain] },
  { name: "chain, named leaf", args: [chain, "b"] },
  { name: "chain, null leaf", args: [chain, null] },
  { name: "chain, unknown leaf falls back to the last entry", args: [chain, "nope"] },
  { name: "chain, empty-string leaf falls back to the last entry", args: [chain, ""] },
  { name: "branch left", args: [branched, "b"] },
  { name: "branch right", args: [branched, "y"] },
  { name: "branch default leaf is the last entry", args: [branched] },
  { name: "dangling parent ends the path", args: [[user("a", "gone", "orphan"), assistant("b", "a", "child")]] },
  { name: "thinking level and model along the path", args: [withSettings] },
  { name: "thinking level and model stop at the leaf", args: [withSettings, "a"] },
  { name: "no settings: off and null", args: [chain.slice(0, 1)] },
  { name: "assistant message sets the model", args: [[user("a", null, "q"), assistant("b", "a", "r", "p", "m")]] },
  { name: "model_change without a provider", args: [[{ ...base("m", null, "model_change"), modelId: "only" }]] },
  { name: "thinking_level_change without a level", args: [[base("t", null, "thinking_level_change")]] },
  { name: "assistant without provider", args: [[{ ...base("b", null, "message"), message: { role: "assistant", content: [], model: "m" } }]] },
  {
    name: "compaction keeps from firstKeptEntryId",
    args: [[user("a", null, "one"), assistant("b", "a", "two"), user("c", "b", "three"), compaction("k", "c", "b"), user("e", "k", "after")]],
  },
  {
    name: "compaction drops system messages it keeps",
    args: [[system("s", null, "sys"), user("a", "s", "one"), assistant("b", "a", "two"), compaction("k", "b", "s"), user("e", "k", "after")]],
  },
  {
    name: "compaction whose firstKeptEntryId is not on the path keeps nothing before it",
    args: [[user("a", null, "one"), assistant("b", "a", "two"), compaction("k", "b", "missing"), user("e", "k", "after")]],
  },
  {
    name: "compaction with a system message",
    args: [[user("a", null, "one"), compaction("k", "a", "a", { systemMessage: { role: "system", content: "new system", timestamp: 9 } }), user("e", "k", "after")]],
  },
  {
    name: "an older compaction inside the kept range is dropped",
    args: [[user("a", null, "one"), compaction("k1", "a", "a"), user("b", "k1", "two"), assistant("c", "b", "three"), compaction("k2", "c", "b"), user("d", "k2", "four")]],
  },
  {
    name: "leaf before the compaction ignores it",
    args: [[user("a", null, "one"), assistant("b", "a", "two"), compaction("k", "b", "a"), user("e", "k", "after")], "b"],
  },
  {
    name: "branch summary",
    args: [[user("a", null, "one"), { ...base("s", "a", "branch_summary"), fromId: "a", summary: "went elsewhere" }, user("b", "s", "next")]],
  },
  {
    name: "empty branch summary adds nothing",
    args: [[user("a", null, "one"), { ...base("s", "a", "branch_summary"), fromId: "a", summary: "" }, user("b", "s", "next")]],
  },
  {
    name: "custom message and plain custom entry",
    args: [[user("a", null, "one"), { ...base("c", "a", "custom_message"), customType: "note", content: "shown", display: true, details: { n: 1 } }, { ...base("d", "c", "custom"), customType: "state", data: { x: 1 } }, user("b", "d", "next")]],
  },
  {
    name: "label and session_info entries add nothing",
    args: [[user("a", null, "one"), { ...base("l", "a", "label"), targetId: "a", label: "x" }, { ...base("i", "l", "session_info"), name: "n" }, user("b", "i", "next")]],
  },
  {
    name: "context_edit removes a message",
    args: [[user("a", null, "one"), assistant("b", "a", "two"), { ...base("e", "b", "context_edit"), targetId: "a", replacement: null }]],
  },
  {
    name: "context_edit replaces a user message's content",
    args: [[user("a", null, "one"), assistant("b", "a", "two"), { ...base("e", "b", "context_edit"), targetId: "a", replacement: { content: "edited" } }]],
  },
  {
    name: "context_edit with a string replaces an assistant message with a text block",
    args: [[user("a", null, "one"), assistant("b", "a", "two"), { ...base("e", "b", "context_edit"), targetId: "b", replacement: { content: "edited" } }]],
  },
  {
    name: "context_edit with blocks replaces an assistant message's content",
    args: [[user("a", null, "one"), assistant("b", "a", "two"), { ...base("e", "b", "context_edit"), targetId: "b", replacement: { content: [{ type: "text", text: "blocks" }] } }]],
  },
  {
    name: "context_edit of a message that is not on the path does nothing",
    args: [[user("a", null, "one"), { ...base("e", "a", "context_edit"), targetId: "zzz", replacement: null }]],
  },
  {
    name: "context_edit leaves a system message alone",
    args: [[system("s", null, "sys"), { ...base("e", "s", "context_edit"), targetId: "s", replacement: { content: "x" } }]],
  },
  {
    name: "message with missing content",
    args: [[{ ...base("a", null, "message"), message: { role: "user", timestamp: 1 } }, { ...base("b", "a", "message"), message: { role: "assistant", timestamp: 1 } }]],
  },
  { name: "message entry without a message throws", args: [[{ ...base("a", null, "message") }]] },
  { name: "message entry with a null message throws", args: [[{ ...base("a", null, "message"), message: null }]] },
  { name: "index passed in", args: [chain, "c", chain.map((entry) => [entry.id, entry])] },
  { name: "index passed in without a leaf falls back to the last entry", args: [chain, "", chain.map((entry) => [entry.id, entry])] },
  { name: "index passed in answers lookups that entries would not", args: [[chain[3]], "b", chain.map((entry) => [entry.id, entry])] },
  { name: "index passed in lacks the parent", args: [chain, "d", [["d", chain[3]]]] },
  { name: "numeric ids", args: [[{ ...user("a", null, "one"), id: 1 }, { ...assistant("b", 1, "two"), id: 2 }]] },
];

export const call = (fn, entries, leafId, byIdPairs) => {
  const byId = byIdPairs === undefined ? undefined : new Map(byIdPairs);
  const json = (value) => JSON.parse(JSON.stringify(value));
  return json(fn(entries, leafId, byId));
};
