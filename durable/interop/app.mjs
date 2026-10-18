// The application both runtimes run: documents, a tool, a task, and a scripted faux model. The Go twin is
// interop_test.go; the two must stay the same program.
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { Type } from "@earendil-works/pi-ai";
import { createModels } from "@earendil-works/pi-ai/models";
import { fauxAssistantMessage, fauxProvider, fauxText, fauxToolCall } from "@earendil-works/pi-ai/providers/faux";
import { AssistantEntry, createRegistry, defineDoc, defineDocFamily, defineExtension, defineTask, defineTool, Harness } from "@earendil-works/pi-durable";
import { openNodeJsonlStorage } from "@earendil-works/pi-durable/storage/jsonl/node";
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";

export const context = BACKGROUND_CONTEXT;
export const CLOCK = 1_700_000_000_000;
// The scripted model answers at once, so no partial reaches a commit in Pi, where the timer cannot fire before the answer's
// microtasks have run. Go's timer runs beside the stream and fires into a stall of 100 ms, which commits a partial no Pi run
// has; an interval no session reaches leaves none. The Go twin is partialIntervalMs in app_test.go.
const PARTIAL_INTERVAL_MS = 3_600_000;

export const Notes = defineDoc({
	kind: "app.notes",
	version: 1,
	scope: "conversation",
	history: "latest",
	fork: "current",
	initial: () => ({ lines: [] }),
});
export const Sessions = defineDoc({
	kind: "app.sessions",
	version: 1,
	scope: "session",
	initial: () => ({ items: {} }),
});

export const Plan = defineDoc({
	kind: "app.plan",
	version: 1,
	scope: "conversation",
	history: "rewindable",
	fork: "asOf",
	initial: () => ({ steps: [] }),
});
export const Blob = defineDocFamily({
	kind: "app.blob",
	version: 1,
	family: true,
	scope: "conversation",
	history: "latest",
	fork: "current",
	initial: (seed) => ({ content: seed.content }),
});

export const Job = defineTask({
	name: "app.job",
	version: 1,
	initial: () => ({ phase: "first" }),
	phases: {
		first: async (_task, runtime, taskContext) => {
			await runtime.commit(() => ({ status: "running", checkpoint: { phase: "second", n: 1 } }), taskContext);
		},
		second: async (task, runtime, taskContext) => {
			await runtime.commit(
				() => ({ status: "terminal", outcome: { status: "completed", result: { n: task.state.checkpoint.n } } }),
				taskContext,
			);
		},
	},
	abort: async (_task, runtime, taskContext) => {
		await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), taskContext);
	},
});

const note = defineTool({
	name: "note",
	description: "Appends a line to the conversation's notes and starts a job",
	parameters: Type.Object({ text: Type.String() }),
	execute: async (args, api, callContext) => {
		const job = await api.commit(async (tx) => {
			(await tx.doc(Notes, api.conversationId)).lines.push(args.text);
			return await tx.createTask(Job, {}, { ownership: { kind: "conversation" } });
		}, callContext);
		// A job left to run beside its tool call commits in an order that is a scheduling accident: Pi's one thread settles
		// it by microtask hops, Go's goroutines by who is ready first. The call ends with the job, so each commit of the
		// session has exactly one commit before it and the raw tables, commit sequences included, are the same in both.
		// PIG_INTEROP_UNAWAITED_JOB=1 leaves the job running beside the call: the D181 evidence test (unawaited_job_test.go).
		if (process.env.PIG_INTEROP_UNAWAITED_JOB !== "1") await api.waitForTask(job, callContext);
		return { output: [{ type: "text", text: `noted ${args.text}` }] };
	},
});

// A tool that runs a child conversation owned by its own task and returns the child's answer.
const spawn = defineTool({
	name: "spawn",
	description: "Runs a child conversation",
	parameters: Type.Object({ task: Type.String() }),
	replay: "safe",
	execute: async (args, api, callContext) => {
		const child = await api.commit(async (tx) => {
			const created = await tx.createConversation({ ownership: { kind: "task", taskId: api.taskId } });
			return created.id;
		}, callContext);
		const handle = await api.conversation(child, callContext);
		const settled = await (
			await handle.submit({ type: "input", content: args.task, requestId: `spawn:${api.taskId}` }, callContext)
		).wait(callContext);
		const entry = await api.commit((tx) => tx.entry(AssistantEntry, settled.answer), callContext);
		return {
			output: [{ type: "text", text: textOf(entry.model[0]) }],
			details: { conversationId: child },
		};
	},
});

export const App = defineExtension({ name: "app", tools: [note, spawn], tasks: [Job] });

function textOf(message) {
	if (message === undefined || message.role === "system") return "";
	if (typeof message.content === "string") return message.content;
	const block = message.content.find((part) => part.type === "text");
	return block?.type === "text" ? block.text : "";
}

/** The scripted model: it answers by the last non-system message, so both runtimes see the same conversation. */
export function route(request) {
	const first = request.messages[0];
	if (first?.role === "system" && typeof first.content === "string" && first.content.includes("summarization")) {
		return fauxAssistantMessage("## Goal\nScripted summary.");
	}
	const last = request.messages.findLast((message) => message.role !== "system");
	if (last?.role === "toolResult") return fauxAssistantMessage("Noted.");
	const text = textOf(last);
	if (text.includes("boom")) return fauxAssistantMessage("", { stopReason: "error", errorMessage: "boom" });
	if (text.includes("delegate")) {
		return fauxAssistantMessage([fauxToolCall("spawn", { task: "child task" }, { id: "call-2" })], { stopReason: "toolUse" });
	}
	if (text.includes("hello")) {
		return fauxAssistantMessage([fauxToolCall("note", { text: "first" }, { id: "call-1" })], { stopReason: "toolUse" });
	}
	return fauxAssistantMessage([fauxText(`Answer to ${text}`)]);
}

export function newModels() {
	const faux = fauxProvider();
	faux.setResponses(Array.from({ length: 200 }, () => route));
	const models = createModels();
	models.setProvider(faux.provider);
	return models;
}

export const model = { provider: "faux", modelId: "faux-1" };

export async function open(path) {
	const registry = createRegistry();
	registry.install(App);
	// A path ending in .sqlite is a SQLite file; any other path is a JSONL directory.
	const storage = path.endsWith(".sqlite") ? await openNodeSqliteStorage(path) : await openNodeJsonlStorage(path, context);
	const harness = await Harness.open(storage, { models: newModels(), registry, now: () => CLOCK, settings: { retry: { enabled: false }, progress: { partialIntervalMs: PARTIAL_INTERVAL_MS } } }, context);
	return { harness, storage };
}

export const say = async (conversation, text) =>
	(await conversation.submit({ type: "input", content: text }, context)).wait(context);
