// Run Pi's context derivation (packages/durable/src/harness/context.ts, imported as-is with types stripped) over programs from stdin: a Session over MemoryStorage that appends entries, and reads of the model context, in full (readContext) and incrementally (readContextFrom, reusing the previous range).
// Program: [{op: "append", model?, edits?: [{target: ref, action, messages?}], head?: ref | "self"} | {op: "read", at?: ref, mode: "full" | "incr"}]; a ref is an index into the entries appended so far (modulo their count; an id that was never issued when there are none).
// stdout: per program, one {id} / {err} per append and one {view: {head, entries, contributions, messages}} / {err} per read.
// usage: node context_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai)
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
const load = (path) => import(pathToFileURL(`${packages}${path}`).href);
const { MemoryStorage } = await load("durable/src/storage/memory.ts");
const { createSession } = await load("durable/src/session/session.ts");
const { readContext, readContextFrom } = await load("durable/src/harness/context.ts");
const { BACKGROUND_CONTEXT } = await load("chord/src/context/index.ts");
const ctx = BACKGROUND_CONTEXT;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const program of JSON.parse(input)) {
	const storage = new MemoryStorage();
	const session = createSession(storage);
	const out = [];
	let conversationId;
	const ids = [];
	let previous;
	const pick = (ref) => (ids.length === 0 ? 1_000_000_000 : ids[ref % ids.length]);
	for (const step of program) {
		try {
			if (step.op === "append") {
				const id = await session.commit(async (tx) => {
					if (conversationId === undefined) conversationId = (await tx.createRootConversation()).id;
					const draft = { kind: step.kind ?? "k" };
					if (step.model !== undefined) draft.model = step.model;
					if (step.edits !== undefined) draft.edits = step.edits.map((edit) => ({ target: pick(edit.target), action: edit.action, ...(edit.messages === undefined ? {} : { messages: edit.messages }) }));
					if (step.head === "self") draft.head = "self";
					else if (step.head !== undefined) draft.head = pick(step.head);
					return (await tx.appendEntry(conversationId, draft)).id;
				}, ctx);
				ids.push(id);
				out.push({ id });
			} else {
				if (conversationId === undefined) {
					out.push({ view: null });
					continue;
				}
				const at = step.at === undefined ? undefined : pick(step.at);
				let view;
				if (step.mode === "full") view = await readContext(session, storage, conversationId, ctx, at);
				else {
					const read = await readContextFrom(session, storage, conversationId, ctx, at, previous);
					view = read.view;
					previous = read.range;
				}
				out.push({ view: { head: view.head?.id ?? null, entries: view.entries.map((entry) => entry.id), contributions: view.contributions, messages: view.messages } });
			}
		} catch (error) {
			out.push({ err: error?.message ?? String(error) });
		}
	}
	results.push(out);
}
process.stdout.write(JSON.stringify(results));
