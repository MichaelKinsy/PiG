// Runs a Session scenario against the pinned Pi's pi-durable and prints, per step, the storage commit and the
// publication, as JSON. The Go test runs the same scenario through durable/core/session and compares.
//   node --conditions=source testdata/oracle.mjs scenarios/<name>.json
// PIDURABLE_SRC selects another pi-durable source tree (default: the pinned mirror, .upstream/current; `make
// upstream-mirror parity-deps` provides it and the dependencies its imports resolve to).
import fs from "node:fs";
const D = process.env.PIDURABLE_SRC ?? new URL("../../../../.upstream/current/packages/durable/src", import.meta.url).pathname;
const { createSession } = await import(D + "/session/session.ts");
const { MemoryStorage } = await import(D + "/storage/memory.ts");
const { defineDoc, defineDocFamily } = await import(D + "/documents.ts");

const scenario = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const clone = (v) => JSON.parse(JSON.stringify(v));

const tokens = {};
for (const [name, d] of Object.entries(scenario.docs ?? {})) {
	const checkpointWhen = d.checkpoint === "always" ? () => true : d.checkpoint === "emptyItems" ? (v) => v.items.length === 0 : undefined;
	const base = { kind: d.kind, version: d.version, scope: d.scope, ...(d.history ? { history: d.history } : {}), ...(d.fork ? { fork: d.fork } : {}), ...(checkpointWhen ? { checkpointWhen } : {}) };
	if (d.migrate) base.migrate = (v) => ({ ...v, ...clone(d.migrate) });
	tokens[name] = d.family
		? defineDocFamily({ ...base, family: true, initial: (seed) => ({ ...clone(d.initial), seed: clone(seed) }) })
		: defineDoc({ ...base, initial: () => clone(d.initial) });
}
const tasks = {};
for (const [name, t] of Object.entries(scenario.tasks ?? {})) {
	tasks[name] = { definition: { name: t.name, version: t.version, initial: (input) => ({ phase: "start", input: clone(input) }) } };
}

const storage = new MemoryStorage();
let clock = 1000;
const session = createSession(storage, { now: () => clock++ });
const commits = [];
const origCommit = storage.commit.bind(storage);
storage.commit = async (writes, context) => {
	const seq = await origCommit(writes, context);
	commits.push({ seq, writes: clone(writes) });
	return seq;
};
const pubs = [];
session.subscribeCommits((p) => pubs.push(clone({ seq: p.seq, changes: p.changes })));

function walk(root, path) {
	let t = root;
	for (const seg of path) t = t[seg];
	return t;
}
function edit(draft, s) {
	const t = walk(draft, s.path);
	switch (s.op) {
		case "push": return t.push(...clone(s.values));
		case "pop": return t.pop();
		case "shift": return t.shift();
		case "unshift": return t.unshift(...clone(s.values));
		case "splice": return t.splice(s.start, s.remove, ...clone(s.items));
		case "setIndex": t[s.index] = clone(s.value); return;
		case "delete": delete t[s.key]; return;
		case "set": t[s.key] = clone(s.value); return;
		default: throw new Error("bad edit " + s.op);
	}
}
function docArgs(d, op) {
	const args = [];
	if (d.scope !== "session") args.push(op.owner);
	if (d.family) { args.push(op.key); if (op.seed !== undefined || true) args.push(op.seed ?? null); }
	return args;
}
const ownership = (o) => (o ? { kind: "task", taskId: o } : { kind: "ownerless" });

const out = [];
for (let i = 0; i < scenario.steps.length; i++) {
	const step = scenario.steps[i];
	const before = commits.length;
	const pubBefore = pubs.length;
	let error;
	const reads = [];
	try {
		await session.commitWith(async (tx) => {
			for (const op of step.ops) {
				switch (op.op) {
					case "rootConversation": await tx.createRootConversation(); break;
					case "createConversation": await tx.createConversation({ ownership: ownership(op.ownerTask) }); break;
					case "fork": await tx.forkConversation(op.parent, op.at, { ownership: ownership(op.ownerTask) }); break;
					case "appendEntry": await tx.appendEntry(op.conversationId, clone(op.draft)); break;
					case "createTask": await tx.createTask(tasks[op.task], clone(op.input), {
						ownership: op.opts?.ownerTask ? { kind: "task", taskId: op.opts.ownerTask } : { kind: "conversation" },
						...(op.opts?.conversationId ? { conversationId: op.opts.conversationId } : {}),
						...(op.opts?.background ? { background: true } : {}),
					}); break;
					case "createSubmission": await tx.createSubmission(clone(op.create)); break;
					case "settle": tx.settleSubmission(op.id, clone(op.settlement)); break;
					case "place": tx.placeSubmission(op.id, op.entry); break;
					case "updateTask": {
						const cur = await storage.task(op.id, {});
						tx.setTask({ ...cur, ...clone(op.patch) });
						break;
					}
					case "doc": {
						const d = scenario.docs[op.doc];
						const draft = await tx.doc(tokens[op.doc], ...docArgs(d, op));
						for (const e of op.edit ?? []) edit(draft, e);
						break;
					}
					case "retireDoc": {
						const d = scenario.docs[op.doc];
						const args = [];
						if (d.scope !== "session") args.push(op.owner);
						if (d.family) args.push(op.key);
						await tx.retireDoc(tokens[op.doc], ...args);
						break;
					}
					case "readTask": try { reads.push(await tx.task(op.id)); } catch (e) { reads.push({ error: e.message }); } break;
					case "readConversation": try { reads.push(await tx.conversation(op.id)); } catch (e) { reads.push({ error: e.message }); } break;
					default: throw new Error("bad op " + op.op);
				}
			}
		}, {}, step.scope);
	} catch (e) {
		error = e.message;
	}
	out.push({ step: i, ...(error === undefined ? {} : { error }), ...(reads.length ? { reads: clone(reads) } : {}), commit: commits.length > before ? commits[commits.length - 1] : null, pub: pubs.length > pubBefore ? pubs[pubs.length - 1] : null });
}
console.log(JSON.stringify(out));
