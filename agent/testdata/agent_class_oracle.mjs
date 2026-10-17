// Run Pi's Agent class (packages/agent/src/agent.ts, imported as-is with types stripped) over scripted programs from stdin.
// A program scripts the stream function, the initial state, the listeners and a sequence of operations (prompt, continue, queues, reset, abort); the output per program is, for every operation, the error it threw and a snapshot of the agent state, plus the events the listeners saw each with a state snapshot.
// usage: node agent_class_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai)
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
const { Agent } = await import(pathToFileURL(`${packages}agent/src/agent.ts`).href);
const { createAssistantMessageEventStream } = await import("@earendil-works/pi-ai");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const ZERO = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
const KEEP = ["role", "content", "stopReason", "errorMessage", "toolCallId", "toolName", "isError", "toolsAdded", "toolsRemoved", "api", "provider", "model", "usage"];
const project = (message) => {
	if (message === undefined) return null;
	const out = {};
	for (const key of KEEP) {
		let value = message[key];
		if (value === undefined) continue;
		if ((key === "toolsAdded" || key === "toolsRemoved") && Array.isArray(value) && value.length === 0) continue;
		out[key] = value;
	}
	return out;
};
const text = (message) => (typeof message.content === "string" ? message.content : message.content.map((block) => block.text).join(""));

const run = async (program) => {
	let call = 0;
	let agent;
	const log = [];
	const assistant = (index, content, stop, extra) => ({ role: "assistant", content, api: "faux", provider: "faux", model: "faux-1", usage: ZERO, stopReason: stop, timestamp: 1000 + index, ...extra });
	const streamFn = (model, llmContext, options) => {
		const index = call++;
		const turn = program.turns[index] ?? { kind: "text", text: "done" };
		if (turn.kind === "throw") throw new Error(turn.text);
		const stream = createAssistantMessageEventStream();
		const aborted = options.signal?.aborted === true;
		let final;
		if (aborted) final = assistant(index, [], "aborted");
		else if (turn.kind === "error") final = assistant(index, [{ type: "text", text: turn.text }], "error", { errorMessage: turn.text });
		else if (turn.kind === "tool") final = assistant(index, [{ type: "toolCall", id: `t${index}`, name: "a", arguments: { q: "x" } }], "toolUse");
		else final = assistant(index, [{ type: "text", text: turn.text }], "stop");
		if (!aborted) stream.push({ type: "start", partial: assistant(index, [], final.stopReason) });
		if (final.stopReason === "error" || final.stopReason === "aborted") stream.push({ type: "error", reason: final.stopReason, error: final });
		else stream.push({ type: "done", reason: final.stopReason, message: final });
		return stream;
	};
	const tools = program.tools.map((name) => ({
		name,
		label: name,
		description: name,
		parameters: { type: "object", properties: { q: { type: "string" } }, required: ["q"] },
		async execute() {
			return { content: [{ type: "text", text: "ok" }], details: {} };
		},
	}));
	const initialState = { model: { id: "faux-1", provider: "faux", api: "faux" }, tools, messages: program.messages };
	if (program.systemPrompt !== null) initialState.systemPrompt = program.systemPrompt;
	agent = new Agent({ initialState, streamFn, steeringMode: program.steeringMode || undefined, followUpMode: program.followUpMode || undefined, toolExecution: "sequential" });
	const snapshot = () => ({
		messages: agent.state.messages.map(project),
		systemPrompt: agent.state.systemPrompt,
		isStreaming: agent.state.isStreaming,
		streaming: project(agent.state.streamingMessage),
		pending: [...agent.state.pendingToolCalls].sort(),
		errorMessage: agent.state.errorMessage ?? null,
		hasSignal: agent.signal !== undefined,
		queued: agent.hasQueuedMessages(),
		peek: agent.peekQueuedMessages().map(text),
		modes: [agent.steeringMode, agent.followUpMode],
	});
	const user = (value) => ({ role: "user", content: value, timestamp: 5 });
	const act = async (action) => {
		switch (action.kind) {
			case "prompt": return agent.prompt(action.text);
			case "promptMessages": return agent.prompt(action.texts.map(user));
			case "continue": return agent.continue();
			case "steer": return agent.steer(user(action.text));
			case "followUp": return agent.followUp(user(action.text));
			case "clearSteering": return agent.clearSteeringQueue();
			case "clearFollowUp": return agent.clearFollowUpQueue();
			case "clearAll": return agent.clearAllQueues();
			case "steeringMode": agent.steeringMode = action.mode; return;
			case "followUpMode": agent.followUpMode = action.mode; return;
			case "reset": return agent.reset();
			case "abort": return agent.abort();
			case "waitForIdle": return agent.waitForIdle();
			case "throw": throw new Error(action.text);
		}
	};
	const eventNumber = [];
	const unsubscribers = [];
	program.listeners.forEach((spec, listenerIndex) => {
		eventNumber[listenerIndex] = 0;
		unsubscribers[listenerIndex] = agent.subscribe(async (event, signal) => {
			const n = eventNumber[listenerIndex]++;
			log.push({ listener: listenerIndex, n, type: event.type, aborted: signal.aborted, ...(spec.snapshot ? { snap: snapshot() } : {}) });
			if (spec.unsubscribeAt === n) unsubscribers[listenerIndex]();
			if (spec.action && spec.action.at === n) await act(spec.action.act);
		});
	});
	const ops = [];
	for (const op of program.ops) {
		let error;
		try {
			await act(op);
		} catch (e) {
			error = e?.message ?? String(e);
		}
		ops.push({ ...(error === undefined ? {} : { error }), state: snapshot() });
	}
	return { log, ops, requests: call };
};
const results = [];
for (const program of JSON.parse(input)) results.push(await run(program));
process.stdout.write(JSON.stringify(results));
