// Oracle for context derivation. It builds random conversations (forks, head markers, edits, tool rounds with
// missing, duplicate and out-of-order results, excluded stop reasons) in pi-durable main's own MemoryStorage,
// derives contexts with its deriveContext(), and writes a replay script the Go tests run:
//   C <hex conversation record>            create a conversation
//   E <conv> <seq> <hex entry record>      append an entry
//   Q <conv> <at> <sha256 hex of expected|!hex err>  derive the context of <conv> cut at <at> (0: newest)
//   S <sha256 hex>                         compaction results over the view of the Q line before it
// The expected text is {"head":<id|null>,"entries":[ids],"contributions":[[msg...]...],"messages":[msg...]}.
import { writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { PI } from "./pimain.mjs";
import { rng, hex } from "./rng.mjs";

const { MemoryStorage } = await import(`${PI}/packages/durable/src/storage/memory.ts`);
const { captureContextBounds, deriveContext } = await import(`${PI}/packages/durable/src/harness/context.ts`);
const { estimateMessageTokens } = await import(`${PI}/packages/ai/src/utils/estimate.ts`);
const { selectCut, estimateContext, summarizedMessages, serializeConversation, summaryPrompt } = await import(`${PI}/packages/durable/src/harness/compaction.ts`);
const { BACKGROUND_CONTEXT } = await import(`${PI}/packages/chord/src/context/index.ts`);

const seed = Number(process.argv[2] ?? 7);
const count = Number(process.argv[3] ?? 300);
const out = process.argv[4] ?? new URL("../testdata/context.hex", import.meta.url).pathname;
const r = rng(seed);

const words = ["alpha", "beta", "gamma", "delta", "é", "日本語", "😀", "line\nbreak", "quote\"d", "tab\t", "\u2028", "x".repeat(40)];
const text = () => {
	if (r.chance(0.04)) return "z".repeat(1997 + r.int(4)) + "\u{1F600}" + "tail" + r.pick(words); // a pair across the truncation cut
	if (r.chance(0.02)) return "w".repeat(2500 + r.int(500));
	return Array.from({ length: 1 + r.int(4) }, () => r.pick(words)).join(" ");
};

// Budgets for selectCut: fixed ones and the exact running sums the walk reaches, with one a half-token either side,
// so the "reaches keepRecentTokens" boundary is tested at equality.
function keepsFor(view) {
	const keeps = [0.5, 30, 100, 400, 2000];
	let kept = 0;
	const sums = [];
	for (let i = view.contributions.length - 1; i >= (view.head === undefined ? 0 : 1); i--) {
		for (const m of view.contributions[i]) kept += estimateMessageTokens(m);
		sums.push(kept);
	}
	for (let i = 0; i < 3 && sums.length > 0; i++) {
		const s = r.pick(sums);
		keeps.push(s, s - 0.5, s + 0.5);
	}
	return keeps;
}

let nextId = 2;
let clock = 1700000000000;
const lines = [];
const storage = new MemoryStorage();
const convs = new Map(); // id -> {parent?, entries: []}
let seq = 1;

async function commit(writes) {
	await storage.commit(writes, BACKGROUND_CONTEXT);
}
async function newConversation(parent) {
	const id = nextId++;
	const rec = parent === undefined ? { id } : { id, parent };
	await commit([{ type: "conversation", value: rec }]);
	convs.set(id, { rec, entries: [] });
	lines.push(`C ${hex(JSON.stringify(rec))}`);
	return id;
}

const usage = () => ({ input: r.int(5000), output: r.int(500), cacheRead: r.int(100), cacheWrite: 0, totalTokens: r.chance(0.7) ? r.int(6000) : 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const stops = ["stop", "toolUse", "toolUse", "length", "stop", "error", "aborted", "deferred", "stop", "stop"];
let callN = 0;
function userMsg() {
	clock += 1 + r.int(50);
	return r.chance(0.5)
		? { role: "user", content: text(), timestamp: clock }
		: { role: "user", content: [{ type: "text", text: text() }, ...(r.chance(0.2) ? [{ type: "image", data: "AAAA", mimeType: "image/png" }] : [])], timestamp: clock };
}
function assistantMsg(calls) {
	clock += 1 + r.int(50);
	const content = [];
	if (r.chance(0.4)) content.push({ type: "thinking", thinking: text() });
	if (r.chance(0.8)) content.push({ type: "text", text: text() });
	for (const c of calls) content.push({ type: "toolCall", id: c.id, name: c.name, arguments: r.chance(0.3) ? { ...c.args, role: "user", stopReason: "aborted", toolCallId: "call_1", type: "toolCall", id: "decoy", content: [{ type: "toolCall", id: "x" }], head: 3, edits: [{ target: 3, action: "omit" }], model: [{ role: "user" }] } : c.args });
	return { role: "assistant", content, api: "faux", provider: "faux", model: "faux-1", usage: usage(), stopReason: r.pick(stops), timestamp: clock };
}
function resultMsg(id, name) {
	clock += 1 + r.int(50);
	return { role: "toolResult", toolCallId: id, toolName: name, content: [{ type: "text", text: text() }], isError: r.chance(0.1), timestamp: clock };
}
function systemMsg() {
	clock += 1 + r.int(50);
	const m = { role: "system", content: r.chance(0.5) ? "" : text(), timestamp: clock };
	if (r.chance(0.7)) m.sections = { [r.pick(["a", "b", "7", "c"])]: r.chance(0.8) ? text() : null };
	if (r.chance(0.4)) m.toolsAdded = [{ name: "t" + r.int(4), description: text(), parameters: { type: "object", properties: {} } }];
	if (r.chance(0.2)) m.toolsRemoved = [{ name: "t" + r.int(4) }];
	return m;
}

// Append one entry to a conversation; visible = ids of entries visible from it (for head and edit targets).
async function addEntry(conv, kind, model, extra, visibleIds) {
	const id = nextId++;
	const rec = { kind };
	if (model !== undefined) rec.model = model;
	if (r.chance(0.2)) rec.data = r.chance(0.5) ? { n: r.int(10), s: text() } : { role: "assistant", stopReason: "error", head: 5, model: [{ role: "toolResult", toolCallId: "call_2" }], edits: [{ target: 4, action: "omit" }], id: 1, conversationId: 99, kind: "decoy", nested: { head: 2 } };
	Object.assign(rec, extra?.pre ?? {});
	rec.id = id;
	rec.conversationId = conv;
	if (extra?.head === "self") rec.head = id;
	else if (extra?.head !== undefined) rec.head = extra.head;
	if (extra?.edits) rec.edits = extra.edits;
	if (r.chance(0.1)) rec.byTaskId = 1000 + r.int(9);
	seq += 1 + r.int(3);
	await commit([{ type: "entry", value: JSON.parse(JSON.stringify(rec)) }]);
	// Stored records keep Pi's key order most of the time; the scanner must not depend on it.
	let stored = rec;
	if (r.chance(0.3)) {
		const e = Object.entries(rec);
		for (let j = e.length - 1; j > 0; j--) {
			const k = r.int(j + 1);
			[e[j], e[k]] = [e[k], e[j]];
		}
		stored = Object.fromEntries(e);
	}
	lines.push(`E ${conv} ${seq} ${hex(JSON.stringify(stored))}`);
	convs.get(conv).entries.push(id);
	visibleIds.push(id);
	return id;
}

function visibleOf(conv) {
	const chain = [];
	let c = convs.get(conv);
	let upper = Infinity;
	const segs = [];
	while (true) {
		segs.push({ entries: c.entries.filter((e) => e <= upper) });
		if (!c.rec.parent) break;
		upper = Math.min(upper, c.rec.parent.at);
		c = convs.get(c.rec.parent.conversationId);
	}
	for (const s of segs.reverse()) chain.push(...s.entries);
	return chain;
}

async function query(conv, at) {
	let text;
	try {
		const bounds = await captureContextBounds(storage, conv, BACKGROUND_CONTEXT, at === 0 ? undefined : at);
		const view = await deriveContext(storage, conv, bounds, BACKGROUND_CONTEXT);
		const j = (m) => JSON.stringify(m);
		text = `{"head":${view.head === undefined ? "null" : view.head.id},"entries":[${view.entries.map((e) => e.id).join(",")}],"contributions":[${view.contributions.map((c) => `[${c.map(j).join(",")}]`).join(",")}],"messages":[${view.messages.map(j).join(",")}]}`;
		lines.push(`Q ${conv} ${at} ${createHash("sha256").update(text).digest("hex")}`);
		if (process.env.DUMP) lines.push(`# ${text}`);
		const keeps = keepsFor(view);
		const cuts = keeps.map((k) => selectCut(view, k) ?? -1);
		const extra = [{ role: "user", content: "hello world", timestamp: 1 }];
		const est = [estimateContext(view, []), estimateContext(view, extra)];
		const first = cuts.find((c) => c >= 0);
		let ser = "", prompt = "";
		if (first !== undefined) {
			const msgs = summarizedMessages(view, first);
			ser = serializeConversation(msgs);
			prompt = summaryPrompt(msgs, "focus \u{1F600} \ud800");
		}
		const s = JSON.stringify({ cuts, est, ser, prompt });
		lines.push(`S ${createHash("sha256").update(s).digest("hex")} ${keeps.join(",")}`);
		if (process.env.DUMP) lines.push(`# ${s}`);
	} catch (e) {
		lines.push(`Q ${conv} ${at} !${hex(String(e.message))}`);
	}
}

async function turn(conv, vis) {
	const k = r.pick([0, 0, 1, 1, 2, 3]);
	const calls = Array.from({ length: k }, () => ({ id: r.chance(0.1) && callN > 0 ? `call_${callN}` : `call_${++callN}`, name: r.pick(["read", "write", "bash"]), args: r.chance(0.2) ? { "2": 1, b: { c: [1, 2.5, null] }, "1": "x" } : { path: text() } }));
	await addEntry(conv, "pi.user", [userMsg()], undefined, vis);
	if (r.chance(0.15)) await addEntry(conv, "pi.system", [systemMsg()], undefined, vis);
	const a = assistantMsg(calls);
	if (r.chance(0.1)) {
		// assistant and one result in one entry
		const model = [a];
		if (calls.length > 0) model.push(resultMsg(calls[0].id, calls[0].name));
		await addEntry(conv, "pi.assistant", model, undefined, vis);
		calls.shift();
	} else {
		await addEntry(conv, "pi.assistant", [a], undefined, vis);
	}
	const order = calls.slice();
	for (let i = order.length - 1; i > 0; i--) {
		const j = r.int(i + 1);
		[order[i], order[j]] = [order[j], order[i]];
	}
	for (const c of order) {
		if (r.chance(0.15)) continue; // missing result
		if (r.chance(0.2)) await addEntry(conv, "pi.user", [userMsg()], undefined, vis); // interleaved user entry
		await addEntry(conv, "pi.tool-result", [resultMsg(r.chance(0.05) ? "orphan_" + r.int(3) : c.id, c.name)], undefined, vis);
		if (r.chance(0.1)) await addEntry(conv, "custom.note", undefined, undefined, vis);
	}
}

for (let s = 0; s < count; s++) {
	const root = await newConversation();
	const rootVis = [];
	let active = [{ conv: root, vis: rootVis }];
	const steps = 3 + r.int(14);
	for (let step = 0; step < steps; step++) {
		const t = r.pick(active);
		const choice = r.int(100);
		if (choice < 55) {
			await turn(t.conv, t.vis);
		} else if (choice < 65 && t.vis.length > 2) {
			// compaction-style head marker
			const vis = visibleOf(t.conv);
			const first = r.pick(vis);
			await addEntry(t.conv, "pi.compaction", [{ role: "user", content: [{ type: "text", text: "summary " + text() }], timestamp: ++clock }], { head: first }, t.vis);
		} else if (choice < 70) {
			await addEntry(t.conv, "pi.reset", r.chance(0.5) ? undefined : [userMsg()], { head: "self" }, t.vis);
		} else if (choice < 82 && t.vis.length > 1) {
			const vis = visibleOf(t.conv);
			const edits = Array.from({ length: 1 + r.int(3) }, () => {
				const target = r.chance(0.1) ? 1 + r.int(nextId) : r.pick(vis);
				return r.chance(0.5) ? { target, action: "omit" } : { target, action: "replace", messages: r.chance(0.2) ? [] : [r.chance(0.5) ? userMsg() : { ...assistantMsg([]), stopReason: r.pick(stops) }] };
			});
			await addEntry(t.conv, r.pick(["pi.system", "custom.edit"]), r.chance(0.5) ? [systemMsg()] : undefined, { edits, head: r.chance(0.15) ? r.pick(vis) : undefined }, t.vis);
		} else if (choice < 92 && t.vis.length > 0) {
			// fork at a visible entry
			const vis = visibleOf(t.conv);
			const at = r.pick(vis);
			const child = await newConversation({ conversationId: t.conv, at });
			active.push({ conv: child, vis: [] });
		} else {
			await addEntry(t.conv, "custom.event", undefined, undefined, t.vis);
		}
		if (r.chance(0.5)) {
			const q = r.pick(active);
			await query(q.conv, 0);
		}
	}
	for (const a of active) {
		await query(a.conv, 0);
		const vis = visibleOf(a.conv);
		for (let i = 0; i < 2 && vis.length > 0; i++) await query(a.conv, r.pick(vis));
		if (r.chance(0.3)) await query(a.conv, 1 + r.int(nextId + 5)); // may be invisible
	}
}
// Long conversations: hundreds of turns with periodic compaction markers, so the settled/open split, head changes and
// extension run over ranges far beyond the short scenarios.
for (let s = 0; s < 2; s++) {
	const conv = await newConversation();
	const vis = [];
	for (let i = 0; i < 180; i++) {
		await turn(conv, vis);
		if (i % 60 === 59) {
			const all = visibleOf(conv);
			await addEntry(conv, "pi.compaction", [{ role: "user", content: [{ type: "text", text: "summary " + i }], timestamp: ++clock }], { head: all[all.length - 20 - r.int(10)] }, vis);
		}
		if (i % 25 === 0) await query(conv, 0);
	}
	await query(conv, 0);
	const all = visibleOf(conv);
	for (let k = 0; k < 4; k++) await query(conv, r.pick(all));
}
writeFileSync(out, lines.join("\n") + "\n");
console.log(lines.length, "lines", nextId, "ids");
