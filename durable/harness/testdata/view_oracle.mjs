// Run Pi's conversation views (packages/durable/src/harness/view.ts, with agent.ts configure and addTools, imported as-is with types stripped) over programs from stdin.
// Program: [{op: "commit", ops: [{t: "append", conv, head?: ref | "self", model?} | {t: "configure", change} | {t: "addTools", names}]} | {op: "attach", label} | {op: "detach", label}]
// A ref is an index into the entries appended to the root conversation (modulo their count). Conversation 0 is the root, 1 another one the views must ignore. `change` carries model-free fields; extensions and tools are name lists, lists or edits.
// stdout: per program, one {ok} / {err} per step, then {views: {label: {initial, events: [{step, value, ops}]}}}.
// usage: node view_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
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
const { ConversationViews } = await load("durable/src/harness/view.ts");
const { configure, addTools } = await load("durable/src/harness/agent.ts");
const { BACKGROUND_CONTEXT } = await load("chord/src/context/index.ts");
const ctx = BACKGROUND_CONTEXT;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const clone = (value) => JSON.parse(JSON.stringify(value));
const named = (items) => items.map((name) => ({ name }));
const asChange = (change) => {
	const out = { ...change };
	if (Array.isArray(change.extensions)) out.extensions = named(change.extensions);
	else if (change.extensions !== undefined && change.extensions !== null) {
		out.extensions = { ...(change.extensions.add === undefined ? {} : { add: named(change.extensions.add) }), ...(change.extensions.remove === undefined ? {} : { remove: named(change.extensions.remove) }) };
	}
	if (Array.isArray(change.tools)) out.tools = named(change.tools);
	else if (change.tools !== undefined && change.tools !== null) out.tools = { remove: named(change.tools.remove) };
	return out;
};
const results = [];
for (const program of JSON.parse(input)) {
	const storage = new MemoryStorage();
	const session = createSession(storage);
	const views = new ConversationViews(session, storage);
	const out = [];
	const conversations = [];
	const entries = [];
	const attached = new Map();
	const log = {};
	const pick = (ref) => (entries.length === 0 ? 1_000_000_000 : entries[ref % entries.length]);
	let current = 0;
	for (const [step, op] of program.entries()) {
		current = step;
		try {
			if (op.op === "commit") {
				const added = [];
				await session.commit(async (tx) => {
					if (conversations.length === 0) {
						conversations.push((await tx.createRootConversation()).id);
						conversations.push((await tx.createConversation({ ownership: { kind: "ownerless" } })).id);
					}
					for (const item of op.ops) {
						if (item.t === "append") {
							const draft = { kind: "k" };
							if (item.model !== undefined) draft.model = item.model;
							if (item.head === "self") draft.head = "self";
							else if (item.head !== undefined) draft.head = pick(item.head);
							const record = await tx.appendEntry(conversations[item.conv], draft);
							if (item.conv === 0) added.push(record.id);
						} else if (item.t === "configure") await configure(tx, conversations[0], asChange(item.change));
						else await addTools(tx, conversations[0], item.names);
					}
				}, ctx);
				entries.push(...added);
				out.push({ ok: true });
			} else if (op.op === "attach") {
				if (conversations.length === 0) throw new Error("no conversation yet");
				const events = [];
				let initial;
				const { detach } = await views.attach(
					conversations[0],
					(value, release) => {
						initial = clone(value);
						return { advance: (next, ops) => events.push({ step: current, value: clone(next), ops: clone(ops) }), end() {}, release };
					},
					ctx,
				);
				attached.set(op.label, detach);
				log[op.label] = { initial, events };
				out.push({ ok: true });
			} else {
				attached.get(op.label)?.();
				attached.delete(op.label);
				out.push({ ok: true });
			}
		} catch (error) {
			out.push({ err: error?.message ?? String(error) });
		}
	}
	out.push({ views: log });
	results.push(out);
}
process.stdout.write(JSON.stringify(results));
