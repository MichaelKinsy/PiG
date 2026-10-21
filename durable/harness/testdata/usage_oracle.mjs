// Run Pi's usage ledger (packages/durable/src/harness/usage.ts, imported as-is with types stripped) over programs from stdin: recordUsage commits on a Session over MemoryStorage, reads of the pi.usage document, and addUsageState sums.
// usage: node usage_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
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
const { UsageDoc, recordUsage, addUsageState } = await load("harness/usage.ts");
// Pi 0ed1b043 (2ec55d80) reads ctx.telemetry in the scheduler and the harness, so an empty object is no longer a Context.
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);
const ctx = BACKGROUND_CONTEXT;

const settle = async (run) => {
	try {
		return { ok: (await run()) ?? null };
	} catch (error) {
		return { err: error?.message ?? String(error) };
	}
};

async function runProgram(program) {
	const storage = new MemoryStorage();
	const session = createSession(storage);
	let conv;
	const results = [];
	for (const [index, step] of program.steps.entries()) {
		results.push({
			index,
			...(await settle(async () => {
				switch (step.op) {
					case "create":
						conv = (await session.commit((tx) => tx.createConversation({ ownership: { kind: "ownerless" } }), ctx)).id;
						return conv;
					case "record":
						await session.commit((tx) => recordUsage(tx, conv ?? 1_000_000_000, step.bucket, step.key, step.usage), ctx);
						return undefined;
					case "read":
						return await session.commit(async (tx) => JSON.parse(JSON.stringify(await tx.doc(UsageDoc, conv ?? 1_000_000_000))), ctx);
					case "sum": {
						const sum = JSON.parse(JSON.stringify(step.sum));
						for (const state of step.states) addUsageState(sum, JSON.parse(JSON.stringify(state)));
						return JSON.parse(JSON.stringify(sum));
					}
					default:
						throw new Error(`unknown op ${step.op}`);
				}
			})),
		});
	}
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
