// Run Pi's MemoryStorage (packages/durable/src/storage/memory.ts, imported as-is with types stripped; @earendil-works/chord resolves to the monorepo sources) over programs from stdin.
// usage: node memory_oracle.mjs <packages dir> [memory | jsonl-write | jsonl-read | sqlite-write | sqlite-read | sqlite-dump]
// memory (default): stdin is an array of programs, each run on a fresh MemoryStorage.
// jsonl-write / jsonl-read: stdin is {programs, dirs}; each program runs on Pi's JSONL storage in its directory (write: every step; read: only the non-commit steps, on a storage reopened from what is already there).
// Program: {steps: [{op, ...}]}. Each step answers {ok: value} or {err: {name, message}}. A scan step may carry cursorFrom (the index of an earlier scan whose `next` becomes its cursor).
import module from "node:module";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		return next(specifier, context);
	},
});
const { defineTask } = await import(pathToFileURL(`${packages}durable/src/tasks.ts`).href);
const { defineDoc, defineDocFamily } = await import(pathToFileURL(`${packages}durable/src/documents.ts`).href);
const { MemoryStorage } = await import(pathToFileURL(`${packages}durable/src/storage/memory.ts`).href);
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);
const { createSession } = await import(pathToFileURL(`${packages}durable/src/session/session.ts`).href);
const ctx = BACKGROUND_CONTEXT;

async function settle(run) {
	try {
		const value = await run();
		return value === undefined ? { ok: null, undef: true } : { ok: value };
	} catch (error) {
		return { err: { name: error?.name ?? "Error", message: error?.message ?? String(error) } };
	}
}

// A settlement answer is the entry ref plus one.
// A tx step runs its ops in one Session commit. An op names an id as {lit: n} or {ref: i}, the i-th conversation or entry the program has created so far (modulo the count; a billion when there is none). Each op answers {ok}, {undef} or {err: message}; ids created in a commit that fails are forgotten.
function pickId(arg, list) {
	if (arg.lit !== undefined) return arg.lit;
	return list.length === 0 ? 1_000_000_000 : list[arg.ref % list.length];
}

const probeTask = defineTask({ name: "probe", version: 1, initial: (input) => ({ phase: "start", input }), phases: {} });

const docTokens = {
	s: defineDoc({ checkpointWhen: () => false, kind: "p.s", version: 1, scope: "session", initial: () => ({ count: 0, items: [] }) }),
	c: defineDoc({ checkpointWhen: () => false, kind: "p.c", version: 1, scope: "conversation", history: "latest", fork: "current", initial: () => ({ count: 0, items: [] }) }),
	i: defineDoc({ checkpointWhen: () => false, kind: "p.i", version: 1, scope: "conversation", history: "latest", fork: "initial", initial: () => ({ count: 0, items: [] }) }),
	r: defineDoc({ checkpointWhen: () => false, kind: "p.r", version: 1, scope: "conversation", history: "rewindable", fork: "asOf", initial: () => ({ count: 0, items: [] }) }),
	k: defineDoc({ checkpointWhen: () => false, kind: "p.k", version: 1, scope: "task", initial: () => ({ count: 0, items: [] }) }),
	g: defineDocFamily({ checkpointWhen: () => false, family: true, kind: "p.g", version: 1, scope: "conversation", history: "rewindable", fork: "asOf", initial: (seed) => ({ seed, count: 0, items: [] }) }),
	f: defineDocFamily({ checkpointWhen: () => false, family: true, kind: "p.f", version: 1, scope: "session", initial: (seed) => ({ seed, count: 0, items: [] }) }),
};

// A doc op's owner argument: the conversation (scope conversation) or task (scope task) the ref names, then the family key and seed.
function docArgs(op, conversations, tasks) {
	const args = [];
	if (op.tok === "c" || op.tok === "i" || op.tok === "r" || op.tok === "g") args.push(pickId(op.owner, conversations));
	if (op.tok === "k") args.push(pickId(op.owner, tasks));
	// noseed: a family access with no seed argument at all (Pi rejects the undefined seed as non-JSON; it is not a JSON null).
	if (op.tok === "f" || op.tok === "g") args.push(...(op.noseed ? [op.key] : [op.key, op.seed]));
	return args;
}

async function runTxOp(tx, op, refs, pending) {
	const conversations = [...refs.conv, ...pending.conv];
	const entries = [...refs.entry, ...pending.entry];
	const tasks = [...refs.task, ...pending.task];
	const submissions = [...refs.sub, ...pending.sub];
	// A task ownership or owner names its task as taskRef: the i-th task created so far.
	const ownership = (value) => (value?.taskRef === undefined ? value : { kind: value.kind, taskId: pickId({ ref: value.taskRef }, tasks) });
	switch (op.t) {
		case "root":
			return await tx.createRootConversation();
		case "create": {
			const record = await tx.createConversation({ ownership: ownership(op.ownership) });
			pending.conv.push(record.id);
			return record;
		}
		case "fork": {
			const parent = pickId(op.parent, conversations);
			// {of: true}: an entry the parent conversation holds, so the fork can succeed.
			const owns = (id) => (refs.owner.get(id) ?? pending.owner.get(id)) === parent;
			// {of: true}: an entry the parent conversation holds; {anc: true}: one it does not hold (visible only through its ancestry).
			const at = op.at.of ? pickId(op.at, entries.filter(owns)) : op.at.anc ? pickId(op.at, entries.filter((id) => !owns(id))) : pickId(op.at, entries);
			const record = await tx.forkConversation(parent, at, { ownership: ownership(op.ownership) });
			pending.conv.push(record.id);
			return record;
		}
		case "append": {
			const draft = { kind: op.draft.kind };
			if (op.draft.data !== undefined) draft.data = op.draft.data;
			if (op.draft.head === "self") draft.head = "self";
			else if (op.draft.head !== undefined) draft.head = pickId(op.draft.head, entries);
			const record = await tx.appendEntry(pickId(op.conv, conversations), draft);
			pending.entry.push(record.id);
			pending.owner.set(record.id, record.conversationId);
			return record;
		}
		case "createTask": {
			const options = { ownership: ownership(op.ownership), background: op.background };
			if (op.conv !== undefined) options.conversationId = pickId(op.conv, conversations);
			const id = await tx.createTask(probeTask, op.input, options);
			pending.task.push(id);
			return id;
		}
		case "createSubmission": {
			const create = { ...op.create, conversationId: pickId(op.conv, conversations) };
			if (op.entry !== undefined) create.entry = pickId(op.entry, entries);
			const record = await tx.createSubmission(create);
			pending.sub.push(record.id);
			return record;
		}
		case "settle": {
			const settlement = op.settlement.answer === undefined ? op.settlement : { ...op.settlement, answer: pickId({ ref: op.settlement.answer - 1 }, entries) };
			return tx.settleSubmission(pickId(op.id, submissions), settlement);
		}
		case "place":
			return tx.placeSubmission(pickId(op.id, submissions), pickId(op.entry, entries));
		case "doc": {
			const draft = await tx.doc(docTokens[op.tok], ...docArgs(op, conversations, tasks));
			for (const mutation of op.mut) {
				if ("set" in mutation) draft.count = mutation.set;
				else if ("push" in mutation) draft.items.push(mutation.push);
				else if ("pop" in mutation) draft.items.pop();
				else if ("del" in mutation) delete draft.count;
			}
			return JSON.parse(JSON.stringify(draft));
		}
		case "retire":
			return await tx.retireDoc(docTokens[op.tok], ...docArgs(op, conversations, tasks));
		case "task":
			return await tx.task(pickId(op.id, tasks));
		case "submission":
			return await tx.submission(pickId(op.id, submissions));
		case "conversation":
			return await tx.conversation(pickId(op.id, conversations));
		case "entry":
			return await tx.entry(pickId(op.id, entries));
		case "scanEntries":
			return await tx.scanEntries(op.query, op.limit);
		case "scanConversations":
			return await tx.scanConversations(op.query, op.limit);
		case "latestHead":
			return await tx.latestHeadMarker(pickId(op.conv, conversations));
		default:
			throw new Error(`unknown tx op ${op.t}`);
	}
}

async function runTx(session, step, refs) {
	const pending = { conv: [], entry: [], owner: new Map(), task: [], sub: [] };
	// step.scope binds the commit like a conversation- or task-scoped runtime commit: the default createTask conversation ({conv}) and the task stamped on appended entries ({task}); each names a created conversation or task.
	const scope = {};
	if (step.scope?.conv !== undefined) scope.conversationId = pickId(step.scope.conv, refs.conv);
	if (step.scope?.task !== undefined) scope.taskId = pickId(step.scope.task, refs.task);
	const result = await settle(async () =>
		await session.commitWith(async (tx) => {
			const outcomes = [];
			for (const op of step.ops) {
				try {
					const value = await runTxOp(tx, op, refs, pending);
					outcomes.push(value === undefined ? { undef: true } : { ok: value });
				} catch (error) {
					outcomes.push({ err: error?.message ?? String(error) });
				}
			}
			if (step.fail) throw new Error("rollback");
			return outcomes;
		}, ctx, scope),
	);
	if (result.err === undefined) {
		refs.conv.push(...pending.conv);
		refs.entry.push(...pending.entry);
		refs.task.push(...pending.task);
		refs.sub.push(...pending.sub);
		for (const [id, conversation] of pending.owner) refs.owner.set(id, conversation);
	}
	return result;
}

async function runProgram(program, storage, readsOnly) {
	const next = [];
	const results = [];
	const refs = { conv: [], entry: [], owner: new Map(), task: [], sub: [] };
	let session;
	for (const [index, step] of program.steps.entries()) {
		if (readsOnly && (step.op === "commit" || step.op === "tx")) continue;
		if (step.op === "tx") {
			session ??= createSession(storage);
			results.push({ index, ...(await runTx(session, step, refs)) });
			continue;
		}
		const cursor = step.cursorFrom === undefined ? step.cursor : next[step.cursorFrom];
		const result = await settle(async () => {
			switch (step.op) {
				case "commit":
					return await storage.commit(step.writes, () => {}, ctx);
				case "mintId":
					return await storage.mintId();
				case "conversation":
					return await storage.conversation(step.id, ctx);
				case "entry":
					return await storage.entry(step.id, ctx);
				case "visibleEntry":
					return await storage.entry(step.conversationId, step.id, ctx);
				case "findLatestHeadMarker":
					return await storage.findLatestHeadMarker(step.conversationId, step.atOrBefore ?? undefined, ctx);
				case "document":
					return await storage.document(step.id, step.at, ctx);
				case "findDocument":
					return await storage.findDocument(step.query, step.at, ctx);
				case "scanDocuments": {
					// scope.ref names the i-th conversation or task a tx step has created (a billion when none).
					const scope = step.query.scope;
					const query = scope.ref === undefined ? step.query : { ...step.query, scope: { kind: scope.kind, [scope.kind === "task" ? "taskId" : "conversationId"]: pickId({ ref: scope.ref }, scope.kind === "task" ? refs.task : refs.conv) } };
					const page = await storage.scanDocuments(query, step.limit, cursor, ctx);
					next[index] = page.next;
					return page;
				}
				case "task":
					return await storage.task(step.id, ctx);
				case "submission":
					return await storage.submission(step.id, ctx);
				case "submissionByRequest":
					return await storage.submissionByRequest(step.conversationId, step.requestId, ctx);
				case "scanTasks": {
					const page = await storage.scanTasks(step.query, step.limit, cursor, ctx);
					next[index] = page.next;
					return page;
				}
				case "scanSubmissions": {
					const page = await storage.scanSubmissions(step.query, step.limit, cursor, ctx);
					next[index] = page.next;
					return page;
				}
				case "scanConversations": {
					const page = await storage.scanConversations(step.query, step.limit, cursor, ctx);
					next[index] = page.next;
					return page;
				}
				case "scanEntries": {
					const page = await storage.scanEntries(step.query, step.limit, cursor, ctx);
					next[index] = page.next;
					return page;
				}
				default:
					throw new Error(`unknown op ${step.op}`);
			}
		});
		results.push({ index, ...result });
	}
	if (session !== undefined) await session.close(ctx);
	return results;
}

const mode = process.argv[3] ?? "memory";
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const parsed = JSON.parse(input);
const out = [];
if (mode === "memory") {
	for (const program of parsed) out.push(await runProgram(program, new MemoryStorage(), false));
} else if (mode === "sqlite-dump") {
	// Every table of each database file, rows in primary-key order, and the schema text: what the file holds, not what a storage reads from it.
	const { DatabaseSync } = await import("node:sqlite");
	for (const dir of parsed.dirs) {
		const db = new DatabaseSync(`${dir}/store.db`, { readOnly: true });
		const schema = db.prepare("SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name").all();
		const tables = {};
		for (const { name } of db.prepare("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name").all()) {
			tables[name] = db.prepare(`SELECT * FROM "${name}" ORDER BY 1, 2`).all().map((row) => ({ ...row }));
		}
		db.close();
		out.push({ schema: schema.map((row) => ({ ...row })), tables });
	}
} else if (mode.startsWith("sqlite")) {
	const { openNodeSqliteStorage } = await import(pathToFileURL(`${packages}durable/src/storage/sqlite/node.ts`).href);
	for (const [i, program] of parsed.programs.entries()) {
		let storage;
		try {
			storage = await openNodeSqliteStorage(`${parsed.dirs[i]}/store.db`);
		} catch (error) {
			out.push([{ index: -1, err: { name: error?.name ?? "Error", message: error?.message ?? String(error) } }]);
			continue;
		}
		out.push(await runProgram(program, storage, mode === "sqlite-read"));
		await storage.close(ctx);
	}
} else {
	const { openNodeJsonlStorage } = await import(pathToFileURL(`${packages}durable/src/storage/jsonl/node.ts`).href);
	for (const [i, program] of parsed.programs.entries()) {
		let storage;
		try {
			storage = await openNodeJsonlStorage(parsed.dirs[i], ctx);
		} catch (error) {
			out.push([{ index: -1, err: { name: error?.name ?? "Error", message: error?.message ?? String(error) } }]);
			continue;
		}
		out.push(await runProgram(program, storage, mode === "jsonl-read"));
		await storage.close(ctx);
	}
}
process.stdout.write(JSON.stringify(out));
