// Run Pi's tool result helpers (packages/durable/src/harness/tool.ts, imported as-is with types stripped; a load hook appends an export of its private helpers) over ops from stdin.
// Op: {op: "bound", content, limits} | {op: "truncated", lines, bytes, retain?} | {op: "fromSlot", slot?, code, message} | {op: "append", call, result, timestamp, durationMs?}
// stdout: per op {ok} or {err}; an append answers the stored entry's {model, data}.
// usage: node tool_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
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
	load(url, context, next) {
		const loaded = next(url, context);
		if (!url.endsWith("/durable/src/harness/tool.ts")) return loaded;
		return { ...loaded, source: `${loaded.source.toString()}\nexport { boundContent, fromSlot, truncated };\n` };
	},
});
const load = (path) => import(pathToFileURL(`${packages}${path}`).href);
const { boundContent, fromSlot, truncated, appendToolResult } = await load("durable/src/harness/tool.ts");
const { MemoryStorage } = await load("durable/src/storage/memory.ts");
const { createSession } = await load("durable/src/session/session.ts");
const { BACKGROUND_CONTEXT } = await load("chord/src/context/index.ts");
const ctx = BACKGROUND_CONTEXT;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		switch (op.op) {
			case "bound":
				out.push({ ok: boundContent(op.content, op.limits) });
				break;
			case "truncated":
				out.push({ ok: truncated({ droppedLines: op.lines, droppedBytes: op.bytes }, op.retain) });
				break;
			case "fromSlot":
				out.push({ ok: fromSlot(op.slot, op.code, op.message) });
				break;
			default: {
				const storage = new MemoryStorage();
				const session = createSession(storage);
				const record = await session.commit(async (tx) => {
					const conversationId = (await tx.createRootConversation()).id;
					return await appendToolResult(tx, conversationId, op.call, op.result, { timestamp: op.timestamp, durationMs: op.durationMs });
				}, ctx);
				out.push({ ok: { model: record.model, data: record.data } });
			}
		}
	} catch (error) {
		out.push({ err: error?.message ?? String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
