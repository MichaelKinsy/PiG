// Run Pi's TaskGraphView (packages/durable/src/harness/task-graph.ts, imported as-is with types stripped) over programs from stdin: Session commits that create conversations and tasks and advance task states, with a long-lived graph watch (advanced by commit publications) and a fresh view (built from storage) read after each step.
// usage: node taskgraph_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
import module from "node:module";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		if (specifier.startsWith("@earendil-works/pi-ai") || specifier === "typebox" || specifier.startsWith("typebox/")) return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const load = (path) => import(pathToFileURL(`${packages}durable/src/${path}`).href);
const { MemoryStorage } = await load("storage/memory.ts");
const { createSession } = await load("session/session.ts");
const { TaskGraphView } = await load("harness/task-graph.ts");
const { defineTask } = await load("tasks.ts");
// Pi 0ed1b043 (2ec55d80) reads ctx.telemetry in the scheduler and the harness, so an empty object is no longer a Context.
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);
const ctx = BACKGROUND_CONTEXT;
const probeTask = defineTask({ name: "probe", version: 1, initial: (input) => ({ phase: "start", input }), phases: {} });
const pick = (list, ref) => (list.length === 0 ? 1_000_000_000 : list[ref % list.length]);
// Nothing here blocks a task: no registry, no restart limit (the Go side passes the same constant answer).
const blocking = { blocked: () => undefined, subscribeBlocked: () => () => {} };
const flush = () => new Promise((resolve) => setImmediate(resolve));

async function stepOp(session, step, convs, tasks) {
	const made = { conv: [], task: [] };
	const value = await session.commitWith(async (tx) => {
		if (step.op === "advance") {
			const id = pick(tasks, step.task);
			const record = await tx.task(id);
			if (record === undefined) throw new Error(`no task ${id}`);
			const checkpoint = { phase: step.phase };
			const state = { pending: { status: "pending", checkpoint }, running: { status: "running", checkpoint }, waiting: { status: "waiting", checkpoint, on: (step.on ?? []).map((ref) => pick(tasks, ref)), policy: step.policy }, completing: { status: "completing", outcome: { status: step.outcome, result: 1 } }, terminal: { status: "terminal", outcome: { status: step.outcome, result: 1 } } }[step.to];
			tx.setTask({ ...record, abortRequested: step.abort ?? record.abortRequested, state });
			return undefined;
		}
		const out = [];
		for (const op of step.ops) {
			if (op.t === "task") {
				const options = { ownership: op.owner === undefined ? { kind: "conversation" } : { kind: "task", taskId: pick([...tasks, ...made.task], op.owner) }, background: op.background };
				if (op.conv !== undefined) options.conversationId = pick([...convs, ...made.conv], op.conv);
				const id = await tx.createTask(probeTask, 1, options);
				made.task.push(id);
				out.push(id);
				// tx.task cannot read after the commit's first table write, so the record is written whole, as the create made it.
				if (op.end !== undefined) tx.setTask({ id, conversationId: options.conversationId, kind: "probe", version: 1, input: 1, background: op.background ?? false, abortRequested: false, state: { status: op.end, outcome: { status: "completed", result: 1 } } });
			} else {
				const ownership = op.owner === undefined ? { kind: "ownerless" } : { kind: "task", taskId: pick([...tasks, ...made.task], op.owner) };
				const record = await tx.createConversation({ ownership });
				made.conv.push(record.id);
				out.push(record.id);
			}
		}
		return out;
	}, ctx);
	return { value, made };
}

async function runProgram(program) {
	const storage = new MemoryStorage();
	const session = createSession(storage);
	const live = await new TaskGraphView(session, storage, blocking).watch(ctx);
	// Each step reports the operations of the frames the long-lived watch's listener received: a commit that changes nothing in the graph delivers nothing.
	let deliveries = [];
	live.start(async (_value, ops) => {
		deliveries.push({ ops });
	});
	const convs = [];
	const tasks = [];
	const results = [];
	for (const [index, step] of program.steps.entries()) {
		let result;
		try {
			const { value, made } = await stepOp(session, step, convs, tasks);
			convs.push(...made.conv);
			tasks.push(...made.task);
			result = { ok: value ?? null };
		} catch (error) {
			result = { err: error?.message ?? String(error) };
		}
		await flush();
		const fresh = await new TaskGraphView(session, storage, blocking).watch(ctx);
		results.push({ index, ...result, live: live.value, fresh: fresh.value, deliveries });
		deliveries = [];
		await fresh.stop();
	}
	await live.stop();
	await session.close(ctx);
	return results;
}

const programs = JSON.parse(await new Promise((resolve) => {
	let input = "";
	process.stdin.on("data", (chunk) => (input += chunk));
	process.stdin.on("end", () => resolve(input));
}));
const out = [];
for (const program of programs) out.push(await runProgram(program));
process.stdout.write(JSON.stringify(out));
