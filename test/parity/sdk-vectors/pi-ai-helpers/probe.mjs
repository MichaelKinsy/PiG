// Runs Pi's own pi-ai helpers (packages/ai/src/utils/{text,transcript,overflow,retry,typebox-helpers}.ts, models.ts, auth/helpers.ts) over fixed inputs and prints, per call,
// the JSON.stringify of what it returned. The Go, Python and Rust SDK tests (and test/extension-conformance's pi_ai_helpers row) feed the same inputs to each SDK's
// port and compare. A call that returns undefined has no "text". Every input is a well-formed value of Pi's TypeScript types.
//
// Usage: node probe.mjs <pinned pi-ai entry> <pinned pi-coding-agent entry> > golden.json
//   e.g. node probe.mjs .upstream/current/node_modules/@earendil-works/pi-ai/dist/index.js .upstream/current/node_modules/@earendil-works/pi-coding-agent/dist/index.js
// The pi-coding-agent entry answers generateDiffString, generateUnifiedPatch, sessionEntryToContextMessages, convertToPng and resizeImage.
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const pi = await import(pathToFileURL(resolve(process.argv[2])).href);
const agent = await import(pathToFileURL(resolve(process.argv[3])).href);
const cases = [];
const call = (fn, ...args) => {
  const input = JSON.parse(JSON.stringify(args));
  const result = pi[fn](...args);
  const text = JSON.stringify(result);
  cases.push(text === undefined ? { fn, args: input } : { fn, args: input, text, result: JSON.parse(text) });
};

const tool = (name, extra = {}) => ({ name, description: `${name} tool`, parameters: { type: "object", properties: { path: { type: "string" } } }, ...extra });
const sys = (content, rest = {}) => ({ role: "system", content, timestamp: 1, ...rest });

// Model type helpers.
const typedModels = [{ id: "a", provider: "p", api: "x" }, { id: "a", provider: "p", api: "x", type: "chat" }, { id: "a", provider: "p", api: "x", type: "image" }, { id: "a", provider: "p", api: "y", type: "classifier" }, { id: "b", provider: "p", api: "x" }, { id: "a", provider: "q", api: "x" }, {}];
for (const model of typedModels) {
  call("getModelType", model);
  for (const type of ["chat", "image", "classifier", "other"]) call("isModelType", model, type);
  for (const api of ["x", "y", "z"]) call("hasApi", model, api);
  for (const other of [...typedModels, undefined]) call("modelsAreEqual", model, other);
}
call("modelsAreEqual", undefined, typedModels[0]);
call("modelsAreEqual", undefined, undefined);

// StringEnum: description and default are kept only when truthy.
call("StringEnum", ["a", "b"]);
call("StringEnum", ["a", "b"], {});
call("StringEnum", ["a", "b"], { description: "d" });
call("StringEnum", ["a", "b"], { default: "a" });
call("StringEnum", ["a", "b"], { description: "d", default: "b" });
call("StringEnum", ["a", "b"], { description: "", default: "" });
call("StringEnum", [], { description: "none" });
call("StringEnum", ["x\u2028y", "é", "😀", "\"q\""], { description: "uni\u2029code" });

// Thinking levels.
const mapped = (thinkingLevelMap, reasoning = true) => ({ id: "m", provider: "p", api: "x", reasoning, ...(thinkingLevelMap === undefined ? {} : { thinkingLevelMap }) });
const models = [
  mapped(undefined, false), mapped(undefined), mapped({}), mapped({ xhigh: "x" }), mapped({ max: "m" }), mapped({ xhigh: "x", max: "m" }),
  mapped({ xhigh: "x", minimal: null }), mapped({ off: null }), mapped({ off: null, minimal: null, low: null }),
  mapped({ off: null, minimal: null, low: null, medium: null, high: null }), mapped({ off: null, minimal: null, low: null, medium: null, high: null, xhigh: "x", max: "m" }),
  mapped({ high: null, xhigh: "x" }), mapped({ medium: "m", high: "h" }), mapped({ xhigh: null, max: null }), mapped({ off: "o", max: "m", xhigh: null }),
  mapped({ off: null, low: null, medium: null, high: null, xhigh: null }, true), mapped({ minimal: null, low: null, medium: null }, false),
];
for (const model of models) {
  call("getSupportedThinkingLevels", model);
  for (const level of ["off", "minimal", "low", "medium", "high", "xhigh", "max", "bogus", "", "OFF"]) call("clampThinkingLevel", model, level);
}

// calculateCost mutates usage.cost and returns it.
const rates = { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75 };
const zeroCost = () => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 });
const usage = (input, output, cacheRead, cacheWrite, extra = {}) => ({ input, output, cacheRead, cacheWrite, totalTokens: input + output + cacheRead + cacheWrite, cost: zeroCost(), ...extra });
const tiered = { ...rates, tiers: [{ inputTokensAbove: 200000, input: 6, output: 22.5, cacheRead: 0.6, cacheWrite: 7.5 }] };
const tiered2 = { ...rates, tiers: [
  { inputTokensAbove: 500000, input: 12, output: 30, cacheRead: 1.2, cacheWrite: 15 },
  { inputTokensAbove: 200000, input: 6, output: 22.5, cacheRead: 0.6, cacheWrite: 7.5 },
  { inputTokensAbove: 200000, input: 99, output: 99, cacheRead: 99, cacheWrite: 99 },
  { inputTokensAbove: 0, input: 1, output: 2, cacheRead: 3, cacheWrite: 4 },
] };
for (const cost of [rates, tiered, tiered2, { ...rates, tiers: [] }, { input: 0.1, output: 0.7, cacheRead: 0.03, cacheWrite: 0.123456789 }, { input: 2.5, output: 10, cacheRead: 0, cacheWrite: 0 }]) {
  const model = { id: "m", provider: "p", api: "x", reasoning: false, cost };
  for (const u of [
    usage(1000, 1000, 500, 300), usage(0, 0, 0, 0), usage(200000, 10, 0, 0), usage(200001, 10, 0, 0), usage(100000, 5, 50000, 50001), usage(499999, 1, 1, 0), usage(500000, 1, 1, 0), usage(500001, 1, 0, 0),
    usage(1000, 1000, 500, 300, { cacheWrite1h: 100 }), usage(1000, 1000, 500, 300, { cacheWrite1h: 300 }), usage(1000, 1000, 500, 300, { cacheWrite1h: 0 }), usage(1, 2, 3, 4, { cacheWrite1h: 400 }),
    usage(123456, 7891, 23456, 789, { cacheWrite1h: 77 }), usage(1, 1, 1, 1), usage(3, 7, 11, 13, { reasoning: 2 }),
  ]) call("calculateCost", model, u);
}

// contentText.
const text = (t) => ({ type: "text", text: t });
const contents = [
  "plain", "", [], [text("a")], [text("a"), text("b")], [text("a"), { type: "image", data: "x", mimeType: "image/png" }, text("b")], [{ type: "thinking", thinking: "t" }, { type: "toolCall", id: "1", name: "n", arguments: {} }],
  [text(""), text("b"), text("")], [text("line1\nline2"), text("😀")],
];
for (const content of contents) { call("contentText", content); call("contentText", content, " | "); call("contentText", content, ""); call("contentText", content, "\u2028"); }

// System message text and transcript replay.
const sections = (value) => value;
const systemMessages = [
  sys("prompt"), sys(""), sys([text("a"), text("b")]), sys("prompt", { sections: { a: "A", b: "B" } }), sys("prompt", { sections: { a: "A", b: null, c: "" } }),
  sys("", { sections: { only: "S" } }), sys("", { sections: { only: null } }), sys("p", { sections: { 2: "two", b: "B", 1: "one" } }), sys("p", { sections: {} }),
];
for (const message of systemMessages) { call("getSystemMessageText", message); call("renderSystemMessageUpdate", message); }

const transcripts = [
  [],
  [{ role: "user", content: "hi", timestamp: 5 }],
  [sys("base", { timestamp: 10, sections: { a: "A", b: "B" }, toolsAdded: [tool("read"), tool("write")] })],
  [sys("base", { timestamp: 10, sections: { a: "A", b: "B" }, toolsAdded: [tool("read"), tool("write")] }), { role: "user", content: "u", timestamp: 11 },
    sys("more", { timestamp: 12, sections: { b: null, c: "C", a: "A2" }, toolsRemoved: [{ name: "read" }], toolsAdded: [tool("grep")] })],
  [sys("base", { timestamp: 10, toolsAdded: [tool("a"), tool("b")] }), sys([text("x")], { timestamp: 11, toolsRemoved: [{ name: "a" }] }), sys("y", { timestamp: 12, toolsAdded: [tool("a", { description: "again" })] })],
  [sys("base", { timestamp: 10, toolsAdded: [tool("a")] }), sys("", { timestamp: 11, toolsAdded: [tool("a", { description: "again" })], toolsRemoved: [{ name: "a" }] })],
  [{ role: "custom", customType: "x", content: "c", timestamp: 1 }, sys("late", { timestamp: 9 }), { role: "bashExecution", command: "ls", timestamp: 2 }],
  [sys("", { timestamp: 0 })],
  [sys("", { timestamp: 7, toolsAdded: [] })],
  [{ role: "user", content: "hi", timestamp: 5 }, sys("late", { timestamp: 3, sections: { 10: "ten", 9: "nine", z: "Z", 1: "one" } })],
  [sys("p", { timestamp: 4, sections: { x: "1", y: "2", z: "3" } }), sys("", { timestamp: 5, sections: { x: null, w: "W" } }), sys("", { timestamp: 6, sections: { x: "again" } })],
  [sys("only tools", { timestamp: 8, toolsRemoved: [{ name: "none" }] })],
];
for (const messages of transcripts) {
  call("getCurrentTools", messages);
  call("getCurrentSystemMessage", messages);
  call("getCurrentSystemPrompt", messages);
  call("getInitialSystemMessage", messages);
  call("getDeclaredTools", messages);
  call("hasNonAdditiveToolChanges", messages);
  call("resolveTranscriptTools", messages, true);
  call("resolveTranscriptTools", messages, false);
  call("collapseSystemMessages", { messages });
  call("resolveTranscript", { messages }, messages.length % 2 === 0);
  call("withoutInitialSystemMessage", messages.filter((m) => m.role !== "custom" && m.role !== "bashExecution"));
}
const contexts = [
  { messages: [] }, { messages: [], systemPrompt: "" }, { messages: [], tools: [] }, { messages: [], systemPrompt: "sp" }, { messages: [], tools: [tool("t")] },
  { systemPrompt: "sp", tools: [tool("t"), tool("u")], messages: [{ role: "user", content: "hi", timestamp: 2 }] }, { systemPrompt: "", tools: [tool("t")], messages: [{ role: "user", content: "hi", timestamp: 2 }] },
  { tools: [], messages: [{ role: "user", content: "hi", timestamp: 2 }] }, { systemPrompt: "sp", messages: [{ role: "user", content: "hi", timestamp: 2 }, { role: "assistant", content: [text("a")], timestamp: 3 }] },
];
for (const context of contexts) call("normalizeContext", context);
for (const [prompt, tools] of [[undefined, undefined], ["", []], ["p", undefined], [undefined, [tool("t")]], ["p", [tool("t")]], ["", [tool("t")]], ["p", []]]) call("createInitialSystemMessage", prompt, tools);

// Overflow, retry and delay.
const assistant = (stopReason, errorMessage, extra = {}) => ({ role: "assistant", content: [], api: "x", provider: "p", model: "m", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: zeroCost() }, stopReason, ...(errorMessage === undefined ? {} : { errorMessage }), timestamp: 1, ...extra });
const errors = [
  "prompt is too long: 213462 tokens > 200000 maximum", "Prompt Too Long", "prompt too long", "413 {\"error\":{\"type\":\"request_too_large\",\"message\":\"Request exceeds the maximum size\"}}",
  "Input is too long for requested model", "Your input exceeds the context window of this model", "Requested token count exceeds the model's maximum context length of 131072 tokens",
  "Input length (265330) exceeds model's maximum context length (262144).", "exceeds the maximum context length of 1,000 tokens", "exceeds maximum context length (5)", "The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
  "This model's maximum prompt length is 131072 but the request contains 537812 tokens", "Please reduce the length of the messages or completion", "This endpoint's maximum context length is 8192 tokens. However, you requested about 9000 tokens",
  "Input length 9000 exceeds the maximum allowed input length of 8,192 tokens.", "The input (9000 tokens) is longer than the model's context length (8192 tokens).", "prompt token count of 5000 exceeds the limit of 4096",
  "the request exceeds the available context size, try increasing it", "tokens to keep from the initial prompt is greater than the context length", "invalid params, context window exceeds limit", "Your request exceeded model token limit: 300000 (requested: 400000)",
  "Prompt has 90,000 tokens, but the configured context size is 8,192 tokens", "400 status code (no body)", "413 (no body)", "413 status code (no body)", "Prompt contains 9000 tokens ... too large for model with 8192 maximum context length",
  "{\"code\":\"1261\",\"message\":\"Prompt too long\"}", "Prompt exceeds max length", "model_context_window_exceeded", "prompt too long; exceeded max context length by 100 tokens", "Range of input length should be [1, 8192]",
  "context_length_exceeded", "context length exceeded", "too many tokens", "Token limit exceeded", "ThrottlingException: Too many tokens, please wait before trying again.", "Throttling error: Too many tokens", "Service unavailable: Too many tokens",
  "rate limit: prompt too long", "too many requests, prompt is too long", "Overloaded", "rate limit exceeded (429)", "fetch failed: ECONNRESET", "invalid api key", "boom", "", "prompt is too\nlong", "prompt is too\u2028long",
  "PROMPT IS TOO LONG", "prompt  is too long", "input token count\nexceeds the maximum", "input token count 5 exceeds the maximum", "\u212a", "tokenſ limit exceeded", "token limit exceeded\u0130", "exceeds the context window\n",
  "insufficient_quota", "You have exceeded your quota", "quota exceeded: 429", "billing problem 503", "GoUsageLimitError 429", "FreeUsageLimitError rate limit", "Monthly usage limit reached", "available balance is low, overloaded", "out of budget",
  "subscription_sharing_usage_limit_exceeded", "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable", "server_busy", "The servers are currently busy", "currently experiencing high demand", "Model is at capacity",
  "rate_limit", "rate-limit", "rate limit", "ratelimit", "rate\nlimit", "rate\u2028limit", "rate\r\nlimit", "Too Many Requests", "HTTP 429", "HTTP 500", "502 Bad Gateway", "503", "504", "520", "524", "1500", "request id 5000000",
  "The service is temporarily unavailable", "service unavailable", "service is unavailable", "service temporarily unavailable", "serviceunavailable", "service\u2003unavailable", "server error", "Internal  Error", "internal_error", "servererror",
  "provider returned error", "Provider returned an error", "The upstream provider returned an error", "providerreturnederror", "exceeded request buffer limit while retrying upstream", "unable to forward upstream", "upstream request failed",
  "upstream_stream_read_error", "stream was interrupted", "http/2 stream failed", "http2 stream failed", "network error", "network_error", "connection error", "connection refused", "connection lost", "other side closed", "fetch failed",
  "request to https://x.example/y failed, reason: socket", "request to  failed, reason:", "request to https://x failed,reason:", "request to https://x failed, reason:", "request to a\u00a0b failed, reason:", "request to a\u2003b failed, reason:", "getaddrinfo ENOTFOUND", "enotfound", "EAI_AGAIN",
  "upstream connect error", "reset before headers", "socket hang up", "socket connection was closed unexpectedly", "timed out", "time out", "timeout", "TimedOut", "terminated", "websocket closed", "websocket_error", "websocket-error",
  "stream ended without a terminal event", "Anthropic stream ended before message_stop", "stream ended before a terminal response event", "http2 request did not get a response", "pending stream has been canceled", "retry delay 90000ms exceeds",
  "you can retry your request", "Try your request again", "Please retry your request", "ResourceExhausted", "resourceexhausted", "resource exhausted", "an unrelated failure", "Unauthorized", "400 bad request", "ENOTFOUND\u0131", "ſerver error",
];
for (const errorMessage of errors) {
  for (const stopReason of ["error", "stop", "length", "aborted", "toolUse"]) {
    if (stopReason !== "error" && !["boom", "Overloaded", "prompt is too long: 213462 tokens > 200000 maximum"].includes(errorMessage)) continue;
    call("isRetryableAssistantError", assistant(stopReason, errorMessage));
    call("isContextOverflow", assistant(stopReason, errorMessage));
    if (stopReason !== "error" || errors.indexOf(errorMessage) < 4) call("isContextOverflow", assistant(stopReason, errorMessage), 200000);
    if (stopReason === "error" && /body|boom|too long/i.test(errorMessage)) call("isContextOverflow", assistant(stopReason, errorMessage, { provider: "cerebras" }));
  }
}
call("isRetryableAssistantError", assistant("error"));
call("isRetryableAssistantError", assistant("error", ""));
const withUsage = (stopReason, input, output, cacheRead, errorMessage) => assistant(stopReason, errorMessage, { usage: { input, output, cacheRead, cacheWrite: 0, totalTokens: input + output + cacheRead, cost: zeroCost() } });
for (const stopReason of ["stop", "length", "error"]) {
  for (const [input, output, cacheRead] of [[190000, 0, 0], [200000, 0, 0], [200001, 0, 0], [100000, 0, 100001], [198000, 0, 0], [197999, 0, 0], [198001, 5, 0], [250000, 50, 0], [0, 0, 0], [199000, 0, 1000]]) {
    for (const contextWindow of [undefined, 0, 200000, 1]) {
      call("isContextOverflow", withUsage(stopReason, input, output, cacheRead), ...(contextWindow === undefined ? [] : [contextWindow]));
    }
  }
}
for (const stopReason of ["length", "stop"]) for (const output of [0, 100, 8191, 8192, 9000]) for (const desired of [0, 1, 8192, -5, 100000]) call("isRecoverableLength", withUsage(stopReason, 1, output, 0), desired);
for (const attempt of [-1, 0, 1, 2, 3, 5, 10, 12, 20, 40, 60, 1000]) {
  for (const policy of [{ baseDelayMs: 1000 }, { baseDelayMs: 1000, maxDelayMs: 30000 }, { baseDelayMs: 1000, maxAgentDelayMs: 30000 }, { baseDelayMs: 250, maxAgentDelayMs: 100 }, { baseDelayMs: 0, maxAgentDelayMs: 100 }, { baseDelayMs: 1.5, maxAgentDelayMs: 100000 }, { baseDelayMs: 1000, maxAgentDelayMs: 0 }, { baseDelayMs: 7, maxAgentDelayMs: 9007199254740991 }]) call("retryDelayMs", policy, attempt);
}
cases.push({ fn: "DEFAULT_MAX_AGENT_RETRY_DELAY_MS", args: [], text: JSON.stringify(pi.DEFAULT_MAX_AGENT_RETRY_DELAY_MS), result: pi.DEFAULT_MAX_AGENT_RETRY_DELAY_MS });

// envApiKeyAuth: resolve over stored credentials and environment variables.
const signal = new AbortController().signal;
const auth = pi.envApiKeyAuth("Name", ["FIRST", "SECOND"]);
const resolveCases = [
  [undefined, {}], [undefined, { FIRST: "a" }], [undefined, { SECOND: "b" }], [undefined, { FIRST: "a", SECOND: "b" }], [undefined, { FIRST: "", SECOND: "b" }], [undefined, { FIRST: "" }],
  [{ type: "api_key", key: "stored" }, { FIRST: "a" }], [{ type: "api_key", key: "stored", env: { X: "1" } }, {}], [{ type: "api_key", key: "" }, { FIRST: "a" }], [{ type: "api_key" }, { SECOND: "b" }],
  [{ type: "api_key", key: "stored", env: {} }, { FIRST: "a" }],
];
const authCases = [];
for (const [credential, env] of resolveCases) {
  const seen = [];
  const result = await auth.resolve({ ctx: { env: async (name) => { seen.push(name); return env[name]; } }, credential, signal });
  const text = JSON.stringify(result);
  authCases.push({ name: "Name", envVars: ["FIRST", "SECOND"], credential, env, seen, ...(text === undefined ? {} : { text, result: JSON.parse(text) }) });
}
const prompts = [];
const login = await auth.login({ signal, prompt: async (request) => { prompts.push(request); return "typed-key"; } });
const noEnvAuth = pi.envApiKeyAuth("None", []);
const noEnv = await noEnvAuth.resolve({ ctx: { env: async () => "never" }, credential: undefined, signal });
authCases.push({ name: "None", envVars: [], credential: null, env: {}, seen: [], ...(noEnv === undefined ? {} : { text: JSON.stringify(noEnv), result: noEnv }) });

// createAssistantMessageEventStream times the final message: durationMs is added unless the message has one or its timestamp predates the stream. Each scenario names how the
// final message is delivered and what it carries; the answer is whether durationMs ended up on the message and, when the scenario preset one, whether it was kept.
const timing = [];
for (const kind of ["done", "error", "end", "doneThenEnd"]) {
  for (const timestamp of ["now", "old", "absent"]) {
    for (const preset of [null, 0, 7]) {
      const stream = pi.createAssistantMessageEventStream();
      const message = (extra = {}) => ({ role: "assistant", content: [], api: "x", provider: "p", model: "m", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: zeroCost() }, stopReason: kind === "error" ? "error" : "stop", ...(timestamp === "absent" ? {} : { timestamp: timestamp === "now" ? Date.now() : 1 }), ...(preset === null ? {} : { durationMs: preset }), ...extra });
      const final = message();
      const later = message({ timestamp: Date.now() });
      delete later.durationMs;
      if (kind === "done") stream.push({ type: "done", reason: "stop", message: final });
      else if (kind === "error") stream.push({ type: "error", reason: "error", error: final });
      else if (kind === "end") stream.end(final);
      else { stream.push({ type: "done", reason: "stop", message: final }); stream.end(later); }
      timing.push({ kind, timestamp, preset, present: final.durationMs !== undefined, value: final.durationMs === preset ? preset : null, laterTimed: later.durationMs !== undefined });
    }
  }
}

// generateDiffString and generateUnifiedPatch over texts that exercise the line diff: no change, one line added, removed or replaced, changes at either end, a change in the
// middle of a long run (which elides context), CRLF, a missing trailing newline, an empty side, non-ASCII text and several context sizes.
const longText = (n, tag = "line") => Array.from({ length: n }, (_, i) => `${tag} ${i + 1}`).join("\n") + "\n";
const replaceLine = (text, at, value) => text.split("\n").map((line, i) => (i === at ? value : line)).join("\n");
const diffPairs = [
  ["", ""], ["same\n", "same\n"], ["", "new\n"], ["old\n", ""], ["a\nb\nc\n", "a\nB\nc\n"], ["a\nb\nc", "a\nb\nc\n"], ["a\nb\nc\n", "a\nb\nc"],
  [longText(30), replaceLine(longText(30), 15, "changed")], [longText(30), replaceLine(replaceLine(longText(30), 2, "x"), 27, "y")],
  [longText(30), replaceLine(longText(30), 0, "first")], [longText(30), replaceLine(longText(30), 29, "last")], [longText(12), longText(12).replace("line 6\n", "")],
  [longText(12), longText(12).replace("line 6\n", "line 6\ninserted\n")], ["a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\n"], ["a\r\nb\n", "a\nb\r\n"], ["héllo\n😀\n", "héllo\n😀😀\n"],
  [longText(120), replaceLine(longText(120), 60, "mid")], ["x\n".repeat(5), "x\n".repeat(6)], ["one\ntwo\n", "two\none\n"], [longText(9), longText(9, "other")],
];
const diffs = [];
for (const [oldContent, newContent] of diffPairs) {
  for (const contextLines of [undefined, 0, 1, 2, 10]) {
    const args = contextLines === undefined ? [oldContent, newContent] : [oldContent, newContent, contextLines];
    const diff = agent.generateDiffString(...args);
    const patchArgs = contextLines === undefined ? ["src/a.ts", oldContent, newContent] : ["src/a.ts", oldContent, newContent, contextLines];
    diffs.push({ oldContent, newContent, ...(contextLines === undefined ? {} : { contextLines }), diff: diff.diff, firstChangedLine: diff.firstChangedLine ?? null, patch: agent.generateUnifiedPatch(...patchArgs) });
  }
}
diffs.push({ path: "a b/ünï.txt", oldContent: "x\n", newContent: "y\n", patchOnly: true, patch: agent.generateUnifiedPatch("a b/ünï.txt", "x\n", "y\n") });

// sessionEntryToContextMessages: what each entry type contributes. Timestamps are ISO strings, which Date.parse reads, plus one it cannot.
const entryBase = { id: "e1", parentId: null, timestamp: "2026-01-02T03:04:05.678Z" };
const userMessage = { role: "user", content: "hi", timestamp: 7 };
const entryInputs = [
  { type: "message", ...entryBase, message: userMessage }, { type: "message", ...entryBase, message: { role: "user", timestamp: 7 } }, { type: "message", ...entryBase, message: { role: "user", content: null, timestamp: 7 } },
  { type: "message", ...entryBase, message: { role: "assistant", timestamp: 7 } }, { type: "message", ...entryBase, message: { role: "toolResult", toolCallId: "t", timestamp: 7 } },
  { type: "message", ...entryBase, message: { role: "system", timestamp: 7 } }, { type: "message", ...entryBase, message: { role: "system", content: "sys", timestamp: 7 } },
  { type: "message", ...entryBase, message: { role: "bashExecution", command: "ls", timestamp: 7 } }, { type: "message", ...entryBase, message: { role: "custom", timestamp: 7 } },
  { type: "custom_message", ...entryBase, customType: "note", content: "text", display: true }, { type: "custom_message", ...entryBase, customType: "note", content: [{ type: "text", text: "t" }], display: false, details: { a: 1, b: [2] } },
  { type: "custom_message", ...entryBase, customType: "note", display: true }, { type: "custom_message", ...entryBase, customType: "note", content: null, display: true, details: null },
  { type: "custom_message", ...entryBase, timestamp: "not a date", customType: "note", content: "x", display: true }, { type: "custom_message", ...entryBase, timestamp: "2026-01-02", customType: "note", content: "x", display: true },
  { type: "custom_message", ...entryBase, timestamp: "Jan 2, 2026 10:00:00 GMT+0200", customType: "note", content: "x", display: true },
  { type: "branch_summary", ...entryBase, fromId: "f1", summary: "sum" }, { type: "branch_summary", ...entryBase, fromId: "f1", summary: "" }, { type: "branch_summary", ...entryBase, fromId: "f1" },
  { type: "compaction", ...entryBase, summary: "cs", firstKeptEntryId: "k", tokensBefore: 1234 }, { type: "compaction", ...entryBase, summary: "cs", firstKeptEntryId: "k", tokensBefore: 0, systemMessage: { role: "system", content: "s", timestamp: 3 } },
  { type: "compaction", ...entryBase, summary: "", firstKeptEntryId: "k", tokensBefore: 5, systemMessage: null },
  { type: "label", ...entryBase, targetId: "t", label: "l" }, { type: "model_change", ...entryBase, provider: "p", modelId: "m" }, { type: "thinking_level_change", ...entryBase, thinkingLevel: "high" },
  { type: "custom", ...entryBase, customType: "c", data: { x: 1 } }, { type: "session_info", ...entryBase, name: "n" },
];
const entries = entryInputs.map((entry) => ({ entry, messages: JSON.parse(JSON.stringify(agent.sessionEntryToContextMessages(entry))) }));

// convertToPng and resizeImage run Pi's image code (Photon), whose encoders are not byte-identical to PiG's: only what an extension reads of the result is recorded, the MIME type
// and the dimensions, with whether the image decodes.
const B64 = { jpeg: "/9j/2wCEAAMCAgMCAgMDAwMEAwMEBQgFBQQEBQoHBwYIDAoMDAsKCwsNDhIQDQ4RDgsLEBYQERMUFRUVDA8XGBYUGBIUFRQBAwQEBQQFCQUFCRQNCw0UFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFP/AABEIABQAKAMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAAAAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQECAwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQACAQIEBAMEBwUEBAABAncAAQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXxFxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2dri4+Tl5ufo6ery8/T19vf4+fr/2gAMAwEAAhEDEQA/APiPT/h/0/d/pXTad8P/ALv7v9K9w074f/d/d/pXTaf8P+n7r9K/SMTxJ/ePzrJOLtvePD9P+H/T91+ldPp/w/6fuv0r3DT/AIf9P3X6V02n/D/7v7v9K+UxPEn94/oDJOLtvePD9P8Ah/8Ad/d/pWl/wr//AKZ/pX0Jp/w/6fu/0rR/4V//ANM/0r5qrxJ73xH7fguLv3S945TTtFtcD93XT6fotpx+7rG07oK6fT+1fMYmpPuf4+ZJVnp7zNjT9Ftfl/d102naLa4H7usfT/4a6bTugr5XE1J9z+gMkqz095mxp+i2nH7utD+xbX/nnTdP7VoV8zVqT5tz9uwdWp7Je8z/2Q==", png: "iVBORw0KGgoAAAANSUhEUgAAACgAAAAUCAIAAABwJOjsAAAALUlEQVR4nGJhYGhgY2CgP2Jh4GEYEDBq8ajFoxaPWjxq8ajFoxaPWkx/iwEDAPAwAp/6LnIiAAAAAElFTkSuQmCC" };
const imageMeta = (image) => (image ? { mimeType: image.mimeType, ...(image.originalWidth === undefined ? {} : { originalWidth: image.originalWidth, originalHeight: image.originalHeight, width: image.width, height: image.height, wasResized: image.wasResized }), decodes: image.data.length > 0 } : null);
const images = [];
for (const [name, data, mimeType] of [["jpeg", B64.jpeg, "image/jpeg"], ["png", B64.png, "image/png"], ["jpeg-as-png", B64.jpeg, "image/png"], ["garbage", "AAAAAAAA", "image/jpeg"], ["empty", "", "image/jpeg"]]) {
  images.push({ op: "convert", name, data, mimeType, result: imageMeta(await agent.convertToPng(data, mimeType)) });
  for (const options of [undefined, { maxWidth: 20, maxHeight: 20 }, { maxWidth: 1000, maxHeight: 1000 }, { maxWidth: 10, maxHeight: 4000 }, { maxBytes: 100 }]) {
    let result = null;
    try { result = imageMeta(await agent.resizeImage(new Uint8Array(Buffer.from(data, "base64")), mimeType, options)); } catch (error) { result = { error: true }; }
    images.push({ op: "resize", name, data, mimeType, ...(options === undefined ? {} : { options }), result });
  }
}

// One case per line keeps the golden diffable.
const lines = (items) => items.map((item) => "  " + JSON.stringify(item)).join(",\n");
process.stdout.write(`{"cases":[\n${lines(cases)}\n],"auth":{"cases":[\n${lines(authCases)}\n],"login":${JSON.stringify({ prompts, result: login })}},\n"timing":[\n${lines(timing)}\n],\n"diffs":[\n${lines(diffs)}\n],\n"entries":[\n${lines(entries)}\n],\n"images":[\n${lines(images)}\n]}\n`);
