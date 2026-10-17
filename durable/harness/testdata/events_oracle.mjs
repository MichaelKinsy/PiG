// Run Pi's event translation (packages/durable/src/harness/events.ts, imported as-is with types stripped; a load hook appends an export of its private translate) over programs from stdin.
// Program: {docs, held: [taskId], steps: [{ops, changes: [{type: "entry" | "task" | "submission", value}]}]}. Conversation 1 is the watched one; its docs evolve with applyImmutable.
// stdout: per program, per step {skip: true} when the operations do not apply, else {events, held (sorted), snapshot: snapshotOf(after)}.
// usage: node events_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai)
import module from "node:module";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		if (specifier.startsWith("@earendil-works/pi-ai")) return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
	load(url, context, next) {
		const loaded = next(url, context);
		if (!url.endsWith("/durable/src/harness/events.ts")) return loaded;
		return { ...loaded, source: `${loaded.source.toString()}\nexport { translate, snapshotOf };\n` };
	},
});
const load = (path) => import(pathToFileURL(`${packages}${path}`).href);
const { translate, snapshotOf } = await load("durable/src/harness/events.ts");
const { applyImmutable } = await load("chord/src/delta/index.ts");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const program of JSON.parse(input)) {
	const out = [];
	const held = new Set(program.held ?? []);
	let before = { entries: [], docs: program.docs };
	for (const step of program.steps) {
		let after;
		try {
			after = { entries: [], docs: applyImmutable({ docs: before.docs }, step.ops).docs };
		} catch {
			out.push({ skip: true });
			continue;
		}
		const events = translate(1, before, after, step.ops, { seq: 1, changes: step.changes }, held);
		out.push({ events, held: [...held].sort((a, b) => a - b), snapshot: snapshotOf(after) });
		before = after;
	}
	results.push(out);
}
process.stdout.write(JSON.stringify(results));
