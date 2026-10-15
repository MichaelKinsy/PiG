// Oracle for the response classifiers (pi-ai utils/retry.ts, utils/overflow.ts). Lines:
//   C <hex assistant message JSON> <retryable 0|1> <overflow 0|1>
//   D <base> <cap|-> <attempt> <delay>
import { writeFileSync } from "node:fs";
import { PI } from "./pimain.mjs";
import { rng, hex } from "./rng.mjs";

const retry = await import(`${PI}/packages/ai/src/utils/retry.ts`);
const overflow = await import(`${PI}/packages/ai/src/utils/overflow.ts`);
const r = rng(Number(process.argv[2] ?? 3));
const out = process.argv[3] ?? new URL("../testdata/classify.hex", import.meta.url).pathname;
const lines = [];

const fragments = [
	"prompt is too long", "Prompt Too Long: 12", "request_too_large", "exceeds the context window of 8192", "exceeds the model's maximum context length of 8,192 tokens",
	"exceeds maximum context length (128000)", "input token count 9 exceeds the maximum", "maximum prompt length is 500", "reduce the length of the messages", "maximum context length is 4096 tokens",
	"input (123 tokens) is longer than the model's context length (100 tokens)", "exceeds the limit of 4096", "too many tokens", "token limit exceeded", "context_length_exceeded", "context length exceeded",
	"Throttling error: too many requests", "Service unavailable: x", "rate limit", "Rate-limit", "too many requests", "429", "overloaded", "Server_Busy", "servers are currently busy", "ServiceUnavailable", "service unavailable",
	"internal error", "InternalError", "provider returned error", "provider-returned-error", "network error", "connection refused", "connection  lost", "fetch failed", "ENOTFOUND", "timed out", "time out", "timeout", "terminated",
	"websocket closed", "stream ended before message_stop", "retry delay", "please retry your request", "ResourceExhausted", "insufficient_quota", "billing", "quota exceeded", "out of budget", "GoUsageLimitError",
	"400 (no body)", "413 status code (no body)", "400", "502", "x", "", "é😀", "context window exceeds limit", "too large for model with 1000 maximum context length", "prompt has 1,000 tokens, but the configured context size is 500 tokens",
	"range of input length should be", "model_context_window_exceeded", "prompt too long; exceeded max context length",
];
const noise = ["", " ", "\n", "X", " – ", "abc ", "ÉÀ", "İ", "ſ", "K"];
for (let i = 0; i < 700; i++) {
	let text = Array.from({ length: 1 + r.int(3) }, () => r.pick(noise) + r.pick(fragments) + r.pick(noise)).join(r.pick([" ", "\n", ""]));
	if (r.chance(0.3)) text = text.toUpperCase();
	const m = { role: "assistant", content: [], provider: r.pick(["cerebras", "openai", "x"]), model: "m", stopReason: r.pick(["error", "error", "error", "stop", "aborted", "length", "toolUse"]), usage: {} };
	if (r.chance(0.9)) m.errorMessage = text;
	lines.push(`C ${hex(JSON.stringify(m))} ${retry.isRetryableAssistantError(m) ? 1 : 0} ${overflow.isContextOverflow(m) ? 1 : 0}`);
}
for (let i = 0; i < 60; i++) {
	const base = r.pick([0, 1, 500, 2000, 1.5, 1e10, 1e300, 7]);
	const cap = r.chance(0.5) ? undefined : r.pick([0, 100, 60000, 1e12, 5000]);
	const attempt = r.pick([0, 1, 2, 3, 5, 10, 50, 1100, 1.5]);
	const policy = { baseDelayMs: base, ...(cap === undefined ? {} : { maxAgentDelayMs: cap }) };
	lines.push(`D ${base} ${cap ?? "-"} ${attempt} ${retry.retryDelayMs(policy, attempt)}`);
}
writeFileSync(out, lines.join("\n") + "\n");
console.log(lines.length, "lines");
