// Measures, on the pinned Pi's Session over MemoryStorage, the microtask passes between the events of the group commit line
// (docs/group-commit.md): admission to the next job's start, Storage call to publication, publication to the caller's resumption,
// the macrotask between two parts. Every mark is [label, turn, tick]: the turn counts the macrotasks (setImmediate) the writer
// and the script waited for, the tick the passes of a self-requeuing microtask since the turn began.
//   node --conditions=source testdata/gc_ticks.mjs
import { fileURLToPath } from "node:url";
const D = process.env.PIDURABLE_SRC ?? new URL("../../../../.upstream/current/packages/durable/src", import.meta.url).pathname;
const { createSession } = await import(D + "/session/session.ts");
const { MemoryStorage } = await import(D + "/storage/memory.ts");
const { BACKGROUND_CONTEXT } = await import(D + "/../../chord/src/context/index.ts");

let turn = 0, tick = 0, marks = [], counting = false;
const LIMIT = 400;
const spin = () => { if (counting && tick < LIMIT) { tick++; queueMicrotask(spin); } };
const nativeSetImmediate = globalThis.setImmediate;
globalThis.setImmediate = (cb, ...args) => nativeSetImmediate(() => { turn++; tick = 0; if (counting) queueMicrotask(spin); cb(...args); }, ...args);
const mark = (label) => marks.push([label, turn, tick]);
const idle = () => new Promise((resolve) => globalThis.setImmediate(resolve));
const ticks = async (n) => { for (let i = 0; i < n; i++) await null; };
const rootConversation = (tx) => tx.createRootConversation();

import fs from "node:fs";
const probes = JSON.parse(fs.readFileSync(fileURLToPath(new URL("./gc_ticks.json", import.meta.url)), "utf8"));

// One probe: jobs queued together on a fresh Session over MemoryStorage. A job's callback marks its start, waits `delay` ticks, and (kind
// write) appends an entry, which makes the commit; `none` writes nothing, `throw` fails after the append, `read` is a readOnLine job. The
// Storage's commit waits `storageTicks` more ticks, through an async wrapper whose receipts come one tick after the call.
async function probe({ name, jobs, storageTicks = 0, refuseAt, rejectAt }) {
	marks = []; turn = 0; tick = 0;
	const storage = new MemoryStorage();
	const origCommit = storage.commit.bind(storage);
	let stored = 0, armed = false;
	storage.commit = async (batches, receive, ctx) => {
		mark(`call:${batches.length}`);
		await ticks(storageTicks);
		// `refuseAt` and `rejectAt` count the batches stored since the probe began: the batch there is refused, or its call rejects, after the ones
		// before it in the call are durable.
		let cut = batches.length;
		for (let i = 0; i < batches.length; i++) if (armed && stored + i === (refuseAt ?? rejectAt)) cut = i;
		const result = await origCommit(batches.slice(0, cut), (index, seq) => { mark(`receive:${index}`); receive(index, seq); }, ctx);
		stored += cut;
		if (cut < batches.length && rejectAt !== undefined) throw new Error("rejected");
		return cut < batches.length ? { stored: cut, refused: new Error("refused") } : result;
	};
	const session = createSession(storage, { now: () => 1000 });
	session.followCommits((p) => mark(`publish:${p.kind === "stored" ? p.seq : "held"}`));
	const root = await session.commitWith(async (tx) => (await rootConversation(tx)).id, BACKGROUND_CONTEXT);
	await idle(); await idle();
	stored = 0; armed = true;
	marks = []; turn = 0; tick = 0; counting = true; queueMicrotask(spin);
	const settled = [];
	for (const { label, delay = 0, kind, internal = false } of jobs) {
		if (kind === "read") {
			settled.push(session.readOnLine(async () => { mark(`start:${label}`); await ticks(delay); mark(`end:${label}`); }).then(() => mark(`resolved:${label}`)));
			continue;
		}
		const change = async (tx) => {
			mark(`start:${label}`);
			await ticks(delay);
			if (kind === "scan-write") await tx.scanEntries({ conversationId: root }, 10);
			if (kind !== "none") await tx.appendEntry(root, { kind: "pi.user", model: [{ role: "user", content: label, timestamp: 1 }] });
			if (kind === "throw") throw new Error("boom");
			mark(`end:${label}`);
		};
		const p = internal ? session.commitInternal(change, BACKGROUND_CONTEXT) : session.commitWith(change, BACKGROUND_CONTEXT);
		settled.push(p.then(() => mark(`resolved:${label}`), (e) => mark(`rejected:${label}:${e.message}`)));
	}
	await Promise.all(settled);
	for (let i = 0; i < 6; i++) await idle();
	counting = false;
	console.log(JSON.stringify({ name, marks }));
	await new Promise((r) => nativeSetImmediate(r));
}
// A Session that failed rejects promises nobody awaits (`closed`, the writer's own); the probe only reads its marks.
process.on("unhandledRejection", () => {});
for (const p of probes) if (process.env.PROBE === undefined || p.name === process.env.PROBE) await probe(p);
process.exit(0);
