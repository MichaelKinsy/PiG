// Oracle for the compaction text helpers and entry record encoding. Lines:
//   T <hex message> <hex JSON of summaryText or null> <hex JSON of summaryFailure>
//   D <hex draft> <id> <conv> <task|-> <hex expected record>      (the #appendEntry formula of session/transaction.ts)
//   P <hex messages array> <hex JSON instructions|-> <now> <hex summary JSON string> <reason> <hex expected context> <hex expected entry draft>
//   K <hex summarize> <hex retry> <hex summarize after retry>   checkpoint codecs (spreads of src/harness/compaction.ts)
//   O <hex stream options> <maxTokens> <hex sessionId JSON> <hex thinking JSON> <hex expected options>   summarize options spread
//   X <hex message> <attempt> <retryable 0|1> <enabled 0|1> <maxRetries> <delay> <now> <expected decision>   decision on an answer
//   R <hex JSON handoff|-> <now> <hex expected reset draft>
import { writeFileSync } from "node:fs";
import { PI } from "./pimain.mjs";
import { rng, hex } from "./rng.mjs";

const c = await import(`${PI}/packages/durable/src/harness/compaction.ts`);
const { copyJson } = await import(`${PI}/packages/chord/src/json.ts`);
const r = rng(Number(process.argv[2] ?? 11));
const out = process.argv[3] ?? new URL("../testdata/summary.hex", import.meta.url).pathname;

const words = ["alpha", " beta ", "\n", "\t", "é", "😀", "\ud800", "\udc00", "\u2028", "\ufeff", "\u00a0", "\u3000", "x", "\u200a", "\u180e", "\u0085", "\"", "\\"];
const str = () => Array.from({ length: r.int(5) }, () => r.pick(words)).join("");
const lines = [];

function assistant() {
	const content = [];
	for (let i = r.int(5); i > 0; i--) {
		const k = r.int(4);
		if (k === 0) content.push({ type: "text", text: str() });
		else if (k === 1) content.push({ type: "thinking", thinking: str() });
		else if (k === 2 && r.chance(0.3)) content.push({ type: "toolCall", id: "c" + r.int(9), name: r.pick(["read", str()]), arguments: { "2": str(), a: { b: [1, 2.5, str()] }, "1": null, [str()]: r.int(3) } });
		else content.push({ type: "text", text: str() });
	}
	const m = { role: "assistant", content, stopReason: r.pick(["stop", "stop", "stop", "error", "aborted", "length", "toolUse"]) };
	if (r.chance(0.5)) m.errorMessage = r.pick([str(), "", "boom"]);
	return m;
}
for (let i = 0; i < 600; i++) {
	const m = assistant();
	const msg = JSON.stringify(m);
	const t = c.summaryText(JSON.parse(msg));
	const f = c.summaryFailure(JSON.parse(msg));
	lines.push(`T ${hex(msg)} ${hex(JSON.stringify(t ?? null))} ${hex(JSON.stringify(f))}`);
}

// entry records
const drafts = [];
for (let i = 0; i < 600; i++) {
	const d = {};
	const keys = ["kind", "model", "data", "edits", "id", "conversationId", "byTaskId", "head", "extra", "7", "x"];
	for (const k of keys) if (r.chance(0.5)) d[k] = k === "kind" ? "pi.x" : k === "head" ? r.pick(["self", 5, null, 99]) : k === "id" || k === "conversationId" || k === "byTaskId" ? r.int(100) : k === "model" ? [{ role: "user", content: str(), timestamp: 1e12 + r.int(9) }] : { n: r.int(4), s: str(), f: r.pick([0.1, -0, 1e21, 1.5e-7, 5]) };
	if (r.chance(0.4)) { const e = Object.entries(d); for (let j = e.length - 1; j > 0; j--) { const k = r.int(j + 1); [e[j], e[k]] = [e[k], e[j]]; } for (const k of Object.keys(d)) delete d[k]; for (const [k, v] of e) d[k] = v; }
	const id = 1000 + r.int(1000), conv = 2 + r.int(50), task = r.chance(0.5) ? 100 + r.int(50) : undefined;
	const { head, ...rest } = d;
	const rec = copyJson({ ...rest, id, conversationId: conv, head: head === "self" ? id : head, byTaskId: task }, { omitUndefinedProperties: true });
	lines.push(`D ${hex(JSON.stringify(d))} ${id} ${conv} ${task ?? "-"} ${hex(JSON.stringify(rec))}`);
}

// summary request and entry draft
for (let i = 0; i < 100; i++) {
	const msgs = [];
	for (let j = r.int(6); j > 0; j--) {
		const k = r.int(4);
		if (k === 0) msgs.push({ role: "user", content: r.chance(0.5) ? str() : [{ type: "text", text: str() }, { type: "image", data: "x", mimeType: "image/png" }], timestamp: 1 });
		else if (k === 1) msgs.push(assistant());
		else if (k === 2) msgs.push({ role: "toolResult", toolCallId: "c1", toolName: "read", content: [{ type: "text", text: r.chance(0.3) ? "q".repeat(1999) + "\u{1F600}" + "more" + str() : str() }], isError: false, timestamp: 1 });
		else msgs.push({ role: "system", content: str(), timestamp: 1 });
	}
	const instr = r.chance(0.5) ? str() : undefined;
	const now = r.pick([0, 1700000000123, 1.5, 1e21]);
	const summary = str() + "summary";
	const reason = r.pick(["manual", "threshold", "overflow"]);
	const context = { messages: [{ role: "system", content: c.SUMMARIZATION_SYSTEM_PROMPT, timestamp: now }, { role: "user", content: [{ type: "text", text: c.summaryPrompt(msgs, instr) }], timestamp: now }] };
	const firstKept = 10 + r.int(500);
	const draft = { kind: "pi.compaction", head: firstKept, model: [{ role: "user", content: [{ type: "text", text: `${c.SUMMARY_PREFIX}${summary}${c.SUMMARY_SUFFIX}` }], timestamp: now }], data: { reason } };
	lines.push(`P ${hex(JSON.stringify(msgs))} ${instr === undefined ? "-" : hex(JSON.stringify(instr))} ${now} ${hex(JSON.stringify(summary))} ${reason} ${firstKept} ${hex(JSON.stringify(context))} ${hex(JSON.stringify(draft))}`);
}
// reset drafts
for (const [handoff, now] of [[undefined, 5], ["", 1], ["h\ud800i\n", 1700000000000], ["plain", 2.5]]) {
	const entry = { kind: "pi.reset", head: "self", ...(handoff === undefined ? {} : { model: [{ role: "user", content: handoff, timestamp: now }] }) };
	lines.push(`R ${handoff === undefined ? "-" : hex(JSON.stringify(handoff))} ${now} ${hex(JSON.stringify(entry))}`);
}
for (let i = 0; i < 30; i++) {
	const request = { attempt: 1 + r.int(4), model: { provider: r.pick(["faux", "openai"]), modelId: r.pick(["m1", "gpt-x"]) }, thinkingLevel: r.pick(["off", "high"]), streamOptions: r.chance(0.5) ? {} : { temperature: 0.5, maxRetryDelayMs: 1e5, headers: { a: "b" } }, maxTokens: r.pick([1000, 12345.5, 1e21]), tail: 100 + r.int(50), firstKept: 10 + r.int(50) };
	const until = r.pick([1700000000123, 5.5]);
	const sum = { phase: "summarize", ...request };
	const retry = { phase: "retry", ...request, until };
	const { phase: _p, until: _u, ...rest } = retry;
	const next = { phase: "summarize", ...rest, attempt: rest.attempt + 1 };
	lines.push(`K ${hex(JSON.stringify(sum))} ${hex(JSON.stringify(retry))} ${hex(JSON.stringify(next))}`);
}
for (let i = 0; i < 80; i++) {
	const so = {};
	for (const k of ["temperature", "deferred", "cacheRetention", "maxRetryDelayMs", "headers", "signal", "reasoning", "sessionId", "maxTokens", "7"]) {
		if (r.chance(0.4)) so[k] = k === "deferred" ? { a: 1 } : k === "signal" ? "sig" : k === "headers" ? { x: str() } : r.pick([1, "long", 0.5]);
	}
	const maxTokens = r.pick([0, 100, 5e5, 12.5]);
	const sessionId = r.pick(["0190-uuid", str()]);
	const thinking = r.pick(["off", "high", "minimal"]);
	const { deferred: _d, ...forwarded } = so;
	const options = { ...forwarded, cacheRetention: "none", maxTokens, signal: undefined, sessionId, ...(thinking === "off" ? {} : { reasoning: thinking }) };
	lines.push(`O ${hex(JSON.stringify(so))} ${maxTokens} ${hex(JSON.stringify(sessionId))} ${hex(JSON.stringify(thinking))} ${hex(JSON.stringify(options))}`);
}
for (let i = 0; i < 200; i++) {
	const m = assistant();
	const msg = JSON.stringify(m);
	const parsed = JSON.parse(msg);
	const attempt = 1 + r.int(4), retryable = r.chance(0.5), enabled = r.chance(0.7), maxRetries = r.int(4), delay = r.pick([0, 250, 1000.5]), now = 1000;
	const summary = c.summaryText(parsed);
	const retry = parsed.stopReason === "error" && retryable && enabled && attempt <= maxRetries;
	const decision = summary !== undefined ? { d: "place", s: summary } : retry ? { d: "retry", until: now + delay } : { d: "fail", t: c.summaryFailure(parsed) };
	lines.push(`X ${hex(msg)} ${attempt} ${retryable ? 1 : 0} ${enabled ? 1 : 0} ${maxRetries} ${delay} ${now} ${hex(JSON.stringify(decision))}`);
}
writeFileSync(out, lines.join("\n") + "\n");
console.log(lines.length, "lines");
