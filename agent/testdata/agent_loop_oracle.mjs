// Run Pi's runAgentLoop (packages/agent/src/agent-loop.ts, imported as-is with types stripped) over scripted programs from stdin.
// A program scripts the stream function (one assistant message per request), the tools, the hooks and the queues; the output per program is the event sequence, the returned messages and the transcript of every request, all projected to the observable members.
// usage: node agent_loop_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai)
import module from "node:module";
import { pathToFileURL } from "node:url";
import { chordSourceURL } from "../../durable/internal/oraclesupport/chord_resolve.mjs";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		const chordSource = chordSourceURL(packages, specifier);
		if (chordSource) return next(chordSource, context);
		if (specifier.startsWith("@earendil-works/pi-ai")) return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const { runAgentLoop } = await import(pathToFileURL(`${packages}agent/src/agent-loop.ts`).href);
const { createAssistantMessageEventStream } = await import("@earendil-works/pi-ai");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const ZERO = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
const KEEP = ["role", "content", "stopReason", "toolCallId", "toolName", "isError", "toolsAdded", "toolsRemoved", "thinkingLevel"];
const project = (message) => {
	const out = {};
	for (const key of KEEP) {
		let value = message[key];
		if (value === undefined) continue;
		if ((key === "toolsAdded" || key === "toolsRemoved") && Array.isArray(value) && value.length === 0) continue;
		out[key] = value;
	}
	return out;
};
const result = (r) => ({ content: r.content, details: r.details, isError: r.isError === true, terminate: r.terminate === true });

const run = async (program) => {
	const events = [];
	const requests = [];
	const turns = program.turns;
	let call = 0;
	const assistant = (turn, index, content, stop) => ({ role: "assistant", content, api: "faux", provider: "faux", model: "faux-1", usage: ZERO, stopReason: stop, timestamp: 1000 + index, ...(stop === "error" ? { errorMessage: "boom" } : {}) });
	const streamFn = (model, llmContext, options) => {
		requests.push(llmContext.messages.map(project));
		const index = call++;
		const scripted = turns[index];
		const turn = options.signal?.aborted ? { content: [], stop: "aborted", stream: "plain" } : (scripted ?? { content: [{ type: "text", text: "done" }], stop: "stop", stream: "plain" });
		const stream = createAssistantMessageEventStream();
		const final = assistant(turn, index, turn.content, turn.stop);
		if (turn.stream !== "nostart" && !options.signal?.aborted) stream.push({ type: "start", partial: assistant(turn, index, [], turn.stop) });
		if (turn.stream === "deltas" && turn.stream !== "nostart") {
			const partial = [];
			turn.content.forEach((block, i) => {
				if (block.type === "text") {
					partial.push({ type: "text", text: "" });
					stream.push({ type: "text_start", contentIndex: i, partial: assistant(turn, index, structuredClone(partial), turn.stop) });
					partial[i] = block;
					stream.push({ type: "text_delta", contentIndex: i, delta: block.text, partial: assistant(turn, index, structuredClone(partial), turn.stop) });
					stream.push({ type: "text_end", contentIndex: i, content: block.text, partial: assistant(turn, index, structuredClone(partial), turn.stop) });
				} else {
					partial.push({ ...block, arguments: {} });
					stream.push({ type: "toolcall_start", contentIndex: i, partial: assistant(turn, index, structuredClone(partial), turn.stop) });
					partial[i] = block;
					stream.push({ type: "toolcall_end", contentIndex: i, toolCall: block, partial: assistant(turn, index, structuredClone(partial), turn.stop) });
				}
			});
		}
		if (turn.stop === "error" || turn.stop === "aborted") stream.push({ type: "error", reason: turn.stop, error: final });
		else if (turn.stream !== "noend") stream.push({ type: "done", reason: turn.stop, message: final });
		if (turn.stream === "noend") stream.end(final);
		return stream;
	};
	const controller = new AbortController();
	const tools = program.tools.map((spec) => ({
		name: spec.name,
		label: spec.name,
		description: spec.name,
		parameters: { type: "object", properties: { q: { type: "string" } }, required: ["q"] },
		...(spec.mode ? { executionMode: spec.mode } : {}),
		async execute(id, args, signal, onUpdate) {
			const b = spec.behavior;
			for (let i = 0; i < (b.updates ?? 0); i++) onUpdate({ content: [{ type: "text", text: `u${i}` }], details: { i } });
			if (b.abort) controller.abort();
			if (b.kind === "throw") throw new Error(b.text);
			return { content: [{ type: "text", text: b.text }], details: b.details ?? {}, ...(b.kind === "error" ? { isError: true } : {}), ...(b.terminate ? { terminate: true } : {}) };
		},
	}));
	const queue = (lists) => {
		let i = 0;
		return async () => lists[i++] ?? [];
	};
	let finishCall = 0;
	const config = {
		model: { id: "faux-1", provider: "faux", api: "faux" },
		convertToLlm: (messages) => messages.filter((m) => ["user", "assistant", "toolResult", "system"].includes(m.role)),
		getSteeringMessages: queue(program.steering),
		getFollowUpMessages: queue(program.followUps),
		finishTurn: async (turn) => {
			const decision = program.finish[finishCall++];
			return decision === null || decision === undefined ? undefined : { action: decision };
		},
		...(program.execution ? { toolExecution: program.execution } : {}),
		beforeToolCall: async (ctx) => {
			if (ctx.toolCall.id === program.abortAt) controller.abort();
			const h = program.before[ctx.toolCall.id];
			return h === undefined ? undefined : h;
		},
		afterToolCall: async (ctx) => {
			const h = program.after[ctx.toolCall.id];
			return h === undefined ? undefined : h;
		},
	};
	const emit = async (event) => {
		const out = { type: event.type };
		switch (event.type) {
			case "message_start":
			case "message_end":
				out.message = project(event.message);
				break;
			case "message_update":
				out.message = project(event.message);
				out.sub = event.assistantMessageEvent.type;
				break;
			case "tool_execution_start":
				out.id = event.toolCallId; out.name = event.toolName; out.args = event.args;
				break;
			case "tool_execution_update":
				out.id = event.toolCallId; out.name = event.toolName; out.args = event.args; out.partial = result(event.partialResult);
				break;
			case "tool_execution_end":
				out.id = event.toolCallId; out.name = event.toolName; out.result = result(event.result); out.isError = event.isError;
				break;
			case "turn_end":
				out.message = project(event.message); out.toolResults = event.toolResults.map(project);
				break;
			case "agent_end":
				out.messages = event.messages.map(project);
				break;
		}
		events.push(out);
	};
	let error;
	let messages;
	try {
		messages = (await runAgentLoop(program.prompts, { messages: program.context, tools }, config, emit, controller.signal, streamFn)).map(project);
	} catch (e) {
		error = e?.message ?? String(e);
	}
	return { events, messages, requests, ...(error === undefined ? {} : { error }) };
};
const results = [];
for (const program of JSON.parse(input)) results.push(await run(program));
process.stdout.write(JSON.stringify(results));
