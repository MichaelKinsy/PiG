// Run Pi's harness admission (packages/durable/src/harness/{submissions,inbox}.ts, imported as-is with types stripped) over programs from stdin, on a MemoryStorage behind a Session.
// usage: node submissions_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
// Each program is {steps}; a step answers {ok: value} or {err: message}, then {state} lists every entry the program's conversations hold, their documents with each document's current version and value, their submissions and their tasks.
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
const { admitSubmission } = await load("harness/submissions.ts");
const { applyBoundary, prepareBoundary, withdrawQueuedInputs } = await load("harness/inbox.ts");
const { LiveDoc } = await load("harness/live.ts");
// Pi 0ed1b043 (2ec55d80) reads ctx.telemetry in the scheduler and the harness, so an empty object is no longer a Context.
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);
const ctx = BACKGROUND_CONTEXT;

const settle = async (run) => {
	try {
		const value = await run();
		return value === undefined ? { undef: true } : { ok: value };
	} catch (error) {
		return { err: error?.message ?? String(error) };
	}
};
const pick = (list, ref) => (list.length === 0 ? 1_000_000_000 : list[ref % list.length]);

async function runProgram(program) {
	const storage = new MemoryStorage();
	const session = createSession(storage);
	const convs = [];
	let entries = [];
	const results = [];
	for (const [index, step] of program.steps.entries()) {
		const conv = pick(convs, step.conv ?? 0);
		const result = await settle(async () => {
			switch (step.op) {
				case "create":
					return await session.commit(async (tx) => {
						const record = await tx.createConversation({ ownership: { kind: "ownerless" } });
						return record;
					}, ctx);
				case "append":
					return await session.commit((tx) => tx.appendEntry(conv, withHead(step.draft, entries)), ctx);
				case "admit":
					// Pi 0ed1b043 (2ec55d80): admitSubmission answers {id, status}; the status only labels the submit span, so the program compares the id.
					return (await session.commitWith((tx) => admitSubmission(tx, conv, withHead(step.draft, entries, true), step.now, step.modes), ctx)).id;
				case "endRun":
					return await session.commitWith(async (tx) => {
						const live = await tx.doc(LiveDoc, conv);
						delete live.run;
					}, ctx);
				case "boundary":
					return await session.commitWith(async (tx) => {
						const boundary = await prepareBoundary(tx, conv, step.modes);
						return await applyBoundary(tx, boundary, step.at, step.now);
					}, ctx);
				case "withdraw":
					return await session.commitWith((tx) => withdrawQueuedInputs(tx, conv), ctx);
				default:
					throw new Error(`unknown op ${step.op}`);
			}
		});
		if (step.op === "create" && result.ok !== undefined) convs.push(result.ok.id);
		entries = [];
		for (const id of convs) {
			for (let cursor; ; ) {
				const page = await storage.scanEntries({ conversationId: id }, 1000, cursor, ctx);
				entries.push(...page.items.map((item) => item.id));
				if (page.next === undefined) break;
				cursor = page.next;
			}
		}
		results.push({ index, ...result });
	}
	const state = { entries: [], submissions: [], docs: [], values: [], tasks: [] };
	for (const id of convs) {
		for (let cursor; ; ) {
			const page = await storage.scanEntries({ conversationId: id }, 1000, cursor, ctx);
			state.entries.push(...page.items);
			if (page.next === undefined) break;
			cursor = page.next;
		}
		const docs = await storage.scanDocuments({ scope: { kind: "conversation", conversationId: id }, at: "current" }, 1000, undefined, ctx);
		state.docs.push(...docs.items.map((doc) => ({ ...doc, value: undefined })));
		for (const doc of docs.items) {
			const stored = await storage.document(doc.id, "current", ctx);
			state.values.push({ id: doc.id, kind: doc.kind, version: stored.version, value: stored.value });
		}
	}
	for (let id = 1; id <= 80; id++) {
		const submission = await storage.submission(id, ctx);
		if (submission !== undefined) state.submissions.push(submission);
		const task = await storage.task(id, ctx);
		if (task !== undefined) state.tasks.push(task);
	}
	results.push({ state });
	await session.close(ctx);
	return results;
}

// A draft's head or entry head names an entry the program has seen: {ref: n}, or "self".
function withHead(draft, entries, nested = false) {
	const entry = nested ? draft.entry : draft;
	if (entry === undefined || entry.head === undefined || typeof entry.head !== "object") return draft;
	const head = pick(entries, entry.head.ref);
	return nested ? { ...draft, entry: { ...entry, head } } : { ...draft, head };
}

const programs = JSON.parse(await new Promise((resolve) => {
	let input = "";
	process.stdin.on("data", (chunk) => (input += chunk));
	process.stdin.on("end", () => resolve(input));
}));
const out = [];
for (const program of programs) out.push(await runProgram(program));
process.stdout.write(JSON.stringify(out));
