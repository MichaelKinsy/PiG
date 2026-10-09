// Oracle over real pi-durable runs: a Harness over MemoryStorage with the faux provider runs tool rounds, a manual
// compaction, forks, a reset, edits written through submissions, and an error answer. After each phase the stored
// rows (conversation and entry records exactly as the Session wrote them) and every conversation's
// Conversation.context() are dumped in the replay format of gen_context.mjs (C, E, Q and S lines), so the Go store
// derives from rows the real Session produced, not from rows this repository generated. W lines carry a real
// compaction run: the request the summarizer received, the summary it returned, and the entry the task placed.
import { writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { PI } from "./pimain.mjs";
import { hex } from "./rng.mjs";

const T = `${PI}/packages/durable/test`;
const { chatSetup, openChat } = await import(`${T}/chat-support.ts`);
const { addTool, addSection } = await import(`${T}/harness-support.ts`);
const { context } = await import(`${T}/session-support.ts`);
const { MemoryStorage, defineTool } = await import(`${PI}/packages/durable/src/index.ts`);
const { fauxAssistantMessage, fauxToolCall, fauxText, Type } = await import(`${PI}/packages/ai/src/index.ts`);
const { estimateMessageTokens } = await import(`${PI}/packages/ai/src/utils/estimate.ts`);
const { selectCut, estimateContext, summarizedMessages, serializeConversation, summaryPrompt } = await import(`${PI}/packages/durable/src/harness/compaction.ts`);
const out = process.argv[2] ?? new URL("../testdata/real.hex", import.meta.url).pathname;

const lines = [];
const setup = chatSetup({ models: [{ id: "faux-1", contextWindow: 100_000, maxTokens: 900 }] });
let clock = 1_700_000_000_000;
setup.now = () => (clock += 7);
const Echo = Type.Object({ text: Type.Optional(Type.String()) });
addTool(setup.registry, defineTool({ name: "echo", description: "echo", parameters: Echo, execute: async (args) => ({ content: [{ type: "text", text: `echo ${args.text}` }] }) }));
addTool(setup.registry, defineTool({ name: "slow", description: "slow", parameters: Echo, execute: async (args) => ({ content: [{ type: "text", text: `slow ${"y".repeat(50)} ${args.text}` }], details: { n: 1 } }) }));
addSection(setup.registry, "preamble", () => "You are helpful. \u{1F600}", { tag: false });
const storage = new MemoryStorage();
const { harness, root } = await openChat(storage, setup);
setup.settings.stream = { cacheRetention: "none" };
setup.settings.compaction = { enabled: false, reserveTokens: 1000, keepRecentTokens: 150, backgroundTokens: 0 };
setup.settings.retry = { enabled: false, maxRetries: 0, baseDelayMs: 1 };
harness.resume();
await root.configure({ tools: setup.registry.snapshot().tools().map((t) => t.tool) }, context);

// pi-durable 1.0.4 derives contexts without rule 10 (a system message that only user messages precede leads);
// LEAD_SYSTEM=1 applies main's rule to a 1.0.4 view so the same rows, written by 1.0.4, check the main derivation.
function leadWithSystem(messages) {
	const index = messages.findIndex((message) => message.role !== "user");
	if (index <= 0 || messages[index].role !== "system") return messages;
	return [messages[index], ...messages.slice(0, index), ...messages.slice(index + 1)];
}

const seenConv = new Set();
const seenEntry = new Set();
const jm = (m) => JSON.stringify(m);

async function dump(label) {
	const convs = (await storage.scanConversations({}, 1000, undefined, context)).items;
	const newConvs = convs.filter((c) => !seenConv.has(c.id)).sort((a, b) => a.id - b.id);
	const rows = [];
	for (const c of convs) {
		let cursor;
		do {
			const page = await storage.scanEntries({ conversationId: c.id, order: "ascending" }, 500, cursor, context);
			for (const e of page.items) if (e.conversationId === c.id && !seenEntry.has(e.id)) rows.push(e);
			cursor = page.next;
		} while (cursor !== undefined);
	}
	rows.sort((a, b) => a.id - b.id);
	// A conversation must exist before its entries; ids interleave, so emit conversations first, by id.
	const items = [...newConvs.map((c) => ({ id: c.id, c })), ...rows.map((e) => ({ id: e.id, e }))].sort((a, b) => a.id - b.id);
	for (const it of items) {
		if (it.c) {
			seenConv.add(it.c.id);
			lines.push(`C ${hex(JSON.stringify(it.c))}`);
		} else {
			seenEntry.add(it.e.id);
			const seq = (await storage.entry(it.e.id, context)).commitSeq;
			lines.push(`E ${it.e.conversationId} ${seq} ${hex(JSON.stringify(it.e))}`);
		}
	}
	for (const c of convs) {
		const conv = c.id === root.id ? root : await harness.conversation(c.id, context);
		const ats = [0];
		const es = (await conv.entries({}, 1000, undefined, context)).items;
		// 1.0.4's Conversation.context() takes no `at`.
		if (es.length > 3 && !process.env.NO_AT) ats.push(es[Math.floor(es.length / 2)].id, es[1].id);
		for (const at of ats) {
			let view;
			try {
				view = await conv.context(context, at === 0 ? undefined : { at });
				if (process.env.LEAD_SYSTEM) view = { ...view, messages: leadWithSystem(view.messages) };
			} catch (e) {
				lines.push(`Q ${c.id} ${at} !${hex(String(e.message))}`);
				continue;
			}
			const text = `{"head":${view.head === undefined ? "null" : view.head.id},"entries":[${view.entries.map((e) => e.id).join(",")}],"contributions":[${view.contributions.map((x) => `[${x.map(jm).join(",")}]`).join(",")}],"messages":[${view.messages.map(jm).join(",")}]}`;
			lines.push(`Q ${c.id} ${at} ${createHash("sha256").update(text).digest("hex")}`);
			if (process.env.DUMP) lines.push(`# ${text}`);
			// selectCut and estimateContext changed between 1.0.4 and main (spec 8.7 rule 1 and 8.3); a 1.0.4 run
			// checks reading and context derivation only.
			if (process.env.NO_COMPACTION) continue;
			const keeps = [0.5, 30, 100, 400, 2000];
			let kept = 0;
			const sums = [];
			for (let i = view.contributions.length - 1; i >= (view.head === undefined ? 0 : 1); i--) {
				for (const m of view.contributions[i]) kept += estimateMessageTokens(m);
				sums.push(kept);
			}
			for (const s of sums.filter((_, i) => i % 3 === 0)) keeps.push(s, s - 0.5, s + 0.5);
			const cuts = keeps.map((k) => selectCut(view, k) ?? -1);
			const extra = [{ role: "user", content: "hello world", timestamp: 1 }];
			const est = [estimateContext(view, []), estimateContext(view, extra)];
			const first = cuts.find((x) => x >= 0);
			let ser = "", prompt = "";
			if (first !== undefined) {
				const msgs = summarizedMessages(view, first);
				ser = serializeConversation(msgs);
				prompt = summaryPrompt(msgs, "focus \u{1F600} \ud800");
			}
			const s = JSON.stringify({ cuts, est, ser, prompt });
			lines.push(`S ${createHash("sha256").update(s).digest("hex")} ${keeps.join(",")}`);
			if (process.env.DUMP) lines.push(`# ${s}`);
		}
	}
	console.error(label, lines.length);
}

let captured;
const summaryStep = (summary) => (transcript) => {
	captured = JSON.stringify({ messages: transcript.messages });
	return fauxAssistantMessage([fauxText(summary)]);
};
// After a compaction task completes, emit its W line.
async function compactionLine(conv, instructions) {
	const es = (await conv.entries({}, 1000, undefined, context)).items; // newest first
	const i = es.findIndex((e) => e.kind === "pi.compaction");
	const entry = es[i];
	const tail = es[i + 1].id;
	lines.push(`W ${conv.id} ${tail} ${instructions === undefined ? "-" : hex(JSON.stringify(instructions))} ${hex(captured)} ${hex(JSON.stringify(entry))} ${(await storage.entry(entry.id, context)).commitSeq}`);
}

const wait = async (conv, content) => {
	const sub = await conv.submit({ type: "input", content }, context);
	const settled = await sub.wait(context);
	return settled.status;
};
const calls = (...c) => fauxAssistantMessage(c.map(([n, a, id]) => fauxToolCall(n, a, { id })), { stopReason: "toolUse" });
const text = (label, n) => `${label} ${"x".repeat(n)}`;

// 1. a tool round with two parallel calls, then an answer
setup.faux.setResponses([calls(["echo", { text: "a" }, "c1"], ["slow", { text: "b" }, "c2"]), fauxAssistantMessage([fauxText(text("first answer", 300))])]);
console.error(await wait(root, text("question one", 200)));
await dump("round 1");
// 2. more turns, one with an error answer (excluded from context) and one multi-step
setup.faux.setResponses([fauxAssistantMessage("", { stopReason: "error", errorMessage: "boom" })]);
console.error(await wait(root, text("question two", 200)));
setup.faux.setResponses([calls(["echo", { text: "c" }, "c3"]), calls(["slow", { text: "d" }, "c4"]), fauxAssistantMessage([fauxText(text("second answer", 300))])]);
console.error(await wait(root, text("question three", 200)));
await dump("turns");
// 3. manual compaction with a summary
setup.faux.setResponses([summaryStep("SUMMARY of the start \u{1F600}")]);
setup.settings.compaction = { enabled: false, reserveTokens: 1000, keepRecentTokens: 150, backgroundTokens: 0 };
const id = await root.compact("keep the names", context);
console.error((await harness.waitForTask(id, context)).state.outcome);
await dump("compaction");
await compactionLine(root, "keep the names");
setup.faux.setResponses([fauxAssistantMessage([fauxText(text("post compaction answer", 300))])]);
console.error(await wait(root, text("question four", 200)));
await dump("after compaction");
// 4. a fork at an entry before the compaction, and a turn in the fork
const es = (await root.entries({}, 1000, undefined, context)).items.reverse();
const at = es[Math.floor(es.length / 3)].id;
const child = await root.fork(at, { ownership: { kind: "ownerless" } }, context);
setup.faux.setResponses([fauxAssistantMessage([fauxText(text("fork answer", 100))])]);
console.error(await wait(child, text("fork question", 50)));
await dump("fork");
// 5. a second compaction, a reset with a handoff, and edits written through submissions
setup.faux.setResponses([summaryStep("SECOND SUMMARY")]);
const id2 = await root.compact(undefined, context);
console.error((await harness.waitForTask(id2, context)).state.outcome);
await dump("second compaction");
await compactionLine(root, undefined);
await root.reset("handoff \ud800 text", context);
setup.faux.setResponses([fauxAssistantMessage([fauxText(text("after reset", 100))])]);
console.error(await wait(root, text("question five", 200)));
const all = (await root.entries({}, 1000, undefined, context)).items.reverse();
const target = all.find((e) => e.kind === "pi.user").id;
await (await root.submit({ type: "write", entry: { kind: "custom.edit", edits: [{ target, action: "omit" }] } }, context)).wait(context);
await (await root.submit({ type: "write", entry: { kind: "custom.edit2", edits: [{ target: all.at(-1).id, action: "replace", messages: [{ role: "user", content: "replaced", timestamp: 1 }] }] } }, context)).wait(context);
await dump("reset and edits");
const child2 = await root.fork(all.at(-1).id, { ownership: { kind: "ownerless" } }, context);
await dump("second fork");
writeFileSync(out, lines.join("\n") + "\n");
console.error(lines.length, "lines");
process.exit(0);
