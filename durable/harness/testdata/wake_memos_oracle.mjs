// Run Pi's scheduler (packages/durable/src/harness/scheduler.ts, imported as-is with types stripped) over a few task wakes and print the record a woken task reads of itself (`runtime.getTask`), as JSON: `reserved` writes the signal memos of the wake it applies, `{}` for a wake that marks none.
// usage: node wake_memos_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai and typebox)
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
const { Harness } = await load("harness/harness.ts");
const { createRegistry } = await load("harness/registry.ts");
const { defineExtension } = await load("harness/define.ts");
const { defineTask } = await load("tasks.ts");
const { defineSignal } = await load("signals.ts");
const { createModels } = await import(pathToFileURL(`${packages}ai/src/index.ts`).href);
const { BACKGROUND_CONTEXT: ctx } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);

const Answer = defineSignal({ name: "test.answer", version: 1, schema: { type: "object", properties: { text: { type: "string" } }, required: ["text"], additionalProperties: false } });

async function scenario(input, early) {
	const seen = {};
	const Probe = defineTask({
		name: "test.probe",
		version: 1,
		initial: (task) => ({ phase: task.sleep ? "ask" : "woke" }),
		phases: {
			ask: async (task, runtime, context) => {
				const { keys, until } = task.input;
				await runtime.commit(
					() => ({
						status: "sleeping",
						checkpoint: { phase: "woke" },
						...(keys === undefined ? {} : { signals: keys.map((key) => Answer.key(key)) }),
						...(until === undefined ? {} : { until }),
					}),
					context,
				);
			},
			woke: async (_task, runtime, context) => {
				seen.record = await runtime.getTask(runtime.taskId, context);
				await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: true } }), context);
			},
		},
	});
	const registry = createRegistry();
	registry.install(defineExtension({ name: "tasks", tasks: [Probe] }));
	const harness = await Harness.open(new MemoryStorage(), { models: createModels(), registry, onReport: () => {}, now: () => 1_000 }, ctx);
	const root = await harness.root(ctx);
	const id = await root.commit((tx) => tx.createTask(Probe, input, { ownership: { kind: "conversation" } }), ctx);
	if (early !== undefined) await harness.signal(id, Answer, early.key, { text: early.text }, ctx);
	await harness.waitForTask(id, ctx);
	await harness.close(ctx);
	return seen.record;
}

const out = {
	"plain start": await scenario({ sleep: false }),
	"sleep until a time": await scenario({ sleep: true, until: 500 }),
	"sleep on a key until a time": await scenario({ sleep: true, keys: ["a"], until: 500 }),
	"sleep on a key with a value delivered": await scenario({ sleep: true, keys: ["a"] }, { key: "a", text: "early" }),
};
process.stdout.write(JSON.stringify(out));
