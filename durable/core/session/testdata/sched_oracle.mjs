// Runs a scheduler scenario against the pinned Pi's TaskScheduler (pi-durable harness/scheduler.ts) over a Session on
// MemoryStorage and prints the event log as JSON; the Go test runs the same scenario through this package.
//   node --conditions=source testdata/sched_oracle.mjs scenarios/sched/<name>.json
// PIDURABLE_SRC selects another pi-durable source tree (default: the pinned mirror, .upstream/current; `make
// upstream-mirror parity-deps` provides it and the dependencies its imports resolve to).
import fs from "node:fs";
const D = process.env.PIDURABLE_SRC ?? new URL("../../../../.upstream/current/packages/durable/src", import.meta.url).pathname;
const { createSession } = await import(D + "/session/session.ts");
const { MemoryStorage } = await import(D + "/storage/memory.ts");
const { TaskScheduler } = await import(D + "/harness/scheduler.ts");

const { BACKGROUND_CONTEXT } = await import(D + "/../../chord/src/context/index.ts"); // Pi 2ec55d80: a Context carries a telemetry span
const scenario = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const clone = (v) => JSON.parse(JSON.stringify(v));
const log = [];
const storage = new MemoryStorage();
let commitCount = 0; // the Harness clock advances with each applied commit, so the number of now() calls cannot matter
const now = () => 1000 + commitCount;
const session = createSession(storage, { now });
const origCommit = storage.commit.bind(storage);
storage.commit = async (writes, ctx) => {
	const seq = await origCommit(writes, ctx);
	commitCount++;
	log.push({ t: "commit", seq, writes: clone(writes) });
	return seq;
};

// ---- scripted task definitions
const gates = new Map();
function gate(name) {
	let g = gates.get(name);
	if (g === undefined) {
		g = Promise.withResolvers();
		gates.set(name, g);
	}
	return g;
}
const resolveRef = (ref, created) => (typeof ref === "string" && ref.startsWith("$c") ? created[Number(ref.slice(2))] : ref);
function nextState(next, created) {
	if (next === undefined || next === null) return undefined;
	const state = clone(next);
	if (state.on) state.on = state.on.map((r) => resolveRef(r, created));
	return state;
}
async function runSteps(steps, task, rt, ctx) {
	for (const step of steps) {
		if (step.commit) {
			await rt.commit(async (tx, current) => {
				const created = [];
				for (const op of step.commit.ops ?? []) {
					switch (op.op) {
						case "childTask": created.push(await tx.createTask(defs[op.task], clone(op.input ?? null), { ownership: { kind: "task", taskId: task.id } })); break;
						case "task": created.push(await tx.createTask(defs[op.task], clone(op.input ?? null), { ownership: { kind: "conversation" }, ...(op.background ? { background: true } : {}), ...(op.conversationId ? { conversationId: op.conversationId } : {}) })); break;
						case "appendEntry": await tx.appendEntry(task.conversationId, clone(op.draft)); break;
						case "ownedConversation": await tx.createConversation({ ownership: { kind: "task", taskId: task.id } }); break;
						default: throw new Error("bad op " + op.op);
					}
				}
				return nextState(step.commit.next, created);
			}, ctx);
		} else if (step.memo) {
			const winner = await rt.memo(step.memo[0], clone(step.memo[1]), ctx);
			log.push({ t: "memo", task: task.id, name: step.memo[0], winner });
		} else if (step.await) {
			await gate(step.await).promise;
		} else if (step.waitForTask) {
			try { const r = await rt.waitForTask(step.waitForTask, ctx); log.push({ t: "waited", task: task.id, on: step.waitForTask, status: r.state.outcome.status }); } catch (e) { log.push({ t: "waited", task: task.id, on: step.waitForTask, error: e.message }); }
		} else if (step.throw) {
			throw new Error(step.throw);
		} else if (step.log) {
			log.push({ t: "phase", task: task.id, note: step.log });
		} else throw new Error("bad step");
	}
}
const defs = {};
for (const [key, t] of Object.entries(scenario.tasks ?? {})) {
	const phases = {};
	for (const [name, steps] of Object.entries(t.phases)) phases[name] = (task, rt, ctx) => runSteps(steps, task, rt, ctx);
	defs[key] = {
		definition: {
			name: t.name, version: t.version,
			initial: (input) => ({ phase: t.initialPhase ?? "start", input: clone(input) }),
			phases,
			abort: (task, rt, ctx) => runSteps(t.abort ?? [], task, rt, ctx),
			...(t.migrate ? { migrate: (input, checkpoint, from) => ({ input, checkpoint: { ...checkpoint, migratedFrom: from } }) } : {}),
		},
	};
}
let installed = new Map(Object.entries(scenario.initialRegistry ?? Object.keys(defs)).map(([, k]) => [defs[k].definition.name, defs[k]]));
const listeners = new Set();
const registry = {
	snapshot: () => ({ task: (name) => installed.get(name), installed: () => [] }),
	subscribe: (l) => { listeners.add(l); return () => listeners.delete(l); },
};

const scheduler = new TaskScheduler({
	session, storage, registry, models: {},
	agent: async () => ({}), settings: () => ({ contextRetentionMs: 0 }), env: async () => undefined,
	now, writeAtClose: async () => {}, report: (e) => log.push({ t: "report", message: e.message }),
	settleOutcome: async (tx, record, outcome) => { log.push({ t: "settleOutcome", task: record.id, status: outcome.status }); },
	withdrawInputs: async (tx, id) => { log.push({ t: "withdraw", conversation: id }); },
	conversation: async () => undefined, context: BACKGROUND_CONTEXT,
});

const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((r) => setImmediate(r)); };
function watch(name, p) { p.then((v) => log.push({ t: name, ok: v?.state?.outcome?.status ?? true }), (e) => log.push({ t: name, error: e.message })); }

for (let i = 0; i < scenario.cmds.length; i++) {
	const cmd = scenario.cmds[i];
	log.push({ t: "cmd", i, cmd: cmd.cmd });
	try {
		switch (cmd.cmd) {
			case "commit":
				await session.commitWith(async (tx) => {
					for (const op of cmd.ops) {
						switch (op.op) {
							case "rootConversation": await tx.createRootConversation(); break;
							case "createConversation": await tx.createConversation({ ownership: op.ownerTask ? { kind: "task", taskId: op.ownerTask } : { kind: "ownerless" } }); break;
							case "createTask": await tx.createTask(defs[op.task], clone(op.input ?? null), { ownership: op.opts?.ownerTask ? { kind: "task", taskId: op.opts.ownerTask } : { kind: "conversation" }, ...(op.opts?.conversationId ? { conversationId: op.opts.conversationId } : {}), ...(op.opts?.background ? { background: true } : {}) }); break;
							case "appendEntry": await tx.appendEntry(op.conversationId, clone(op.draft)); break;
							case "createSubmission": await tx.createSubmission(clone(op.create)); break;
							default: throw new Error("bad op " + op.op);
						}
					}
				}, BACKGROUND_CONTEXT);
				break;
			case "open": await scheduler.open(BACKGROUND_CONTEXT); break;
			case "resume": scheduler.resume(); break;
			case "abort": watch("abort", scheduler.abort(cmd.id, BACKGROUND_CONTEXT)); break;
			case "abortConversation": watch("abortConversation", scheduler.abortConversation(cmd.id, cmd.background ?? false, BACKGROUND_CONTEXT)); break;
			case "release": for (const g of cmd.gates ?? [cmd.gate]) gate(g).resolve(); break;
			case "waitForTask": watch("waitForTask:" + cmd.id, scheduler.waitForTask(cmd.id, BACKGROUND_CONTEXT)); break;
			case "waitForIdle": watch("waitForIdle:" + (cmd.conv ?? 0), scheduler.waitForIdle(cmd.conv, BACKGROUND_CONTEXT)); break;
			case "registry": {
				installed = new Map();
				for (const k of cmd.installed) installed.set(defs[k].definition.name, defs[k]);
				for (const l of [...listeners]) l();
				break;
			}
			case "flush": break;
			default: throw new Error("bad cmd " + cmd.cmd);
		}
	} catch (e) {
		log.push({ t: "cmdError", i, message: e.message });
	}
	await flush();
}
// Exit only after the pipe has taken the whole log: process.exit() right after console.log drops what the pipe has not drained.
process.stdout.write(`${JSON.stringify(log)}\n`, () => process.exit(0));
