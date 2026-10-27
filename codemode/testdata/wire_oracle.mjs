// Drives the pinned Pi codemode runtime (serveCodemodeRemote, RemoteCodemodeSandbox, InlineCodemodeSandbox, CodemodeSandboxBase.start)
// through the cases of codemode/wire_oracle_test.go and records what Pi does at its seams: the frames of the remote exchange
// (the VM's messages and the client's replies, as protocol.ts and remote.ts define them), the requests a remote sandbox sends,
// the events of a deterministic run, and the outcomes. The Go test runs the same cases against PiG and compares the records.
import { pathToFileURL } from "node:url";

const TIMEOUT = Symbol("timeout");

/** Rejects after ms: a case whose Pi side does not settle is a failure of the harness, not a result. */
const within = (promise, ms = 60000) =>
	Promise.race([promise, new Promise((resolve) => setTimeout(() => resolve(TIMEOUT), ms).unref())]);

const clone = (value) => (value === undefined ? undefined : JSON.parse(JSON.stringify(value)));

/** What a tool kind answers; the Go test implements the same table. */
function toolBehavior(kind, name) {
	switch (kind) {
		case "echo":
			return async (args) => ({ tool: name, args: args ?? null });
		case "fail":
			return async () => {
				throw new Error(`failing ${name}`);
			};
		case "undef":
			return async () => undefined;
		case "number":
			return async () => 42;
		case "hang":
			// Never settles: the execution ends it (close, abort, deadline) and cancels the call.
			return () => new Promise(() => undefined);
		default:
			throw new Error(`unknown tool kind ${kind}`);
	}
}

/** The client's reply to one call of a serve case: the answer a tool of that kind gives, as host.ts frames it. */
function replyFor(kind, call, extra) {
	switch (kind) {
		case "stop":
			return { type: "stop" };
		case "fail":
			return { type: "result", id: call.id, ok: false, payload: `failing ${call.name}`, ...extra };
		case "undef":
			return { type: "result", id: call.id, ok: true, payload: undefined, ...extra };
		case "number":
			return { type: "result", id: call.id, ok: true, payload: "42", ...extra };
		default:
			return {
				type: "result",
				id: call.id,
				ok: true,
				payload: JSON.stringify({ tool: call.name, args: call.args === undefined ? null : JSON.parse(call.args) }),
				...extra,
			};
	}
}

const resultJson = (result) => ({
	ok: result.ok,
	...(result.ok ? { value: result.value === undefined ? { undefined: true } : result.value, storeWrites: result.storeWrites } : { error: result.error }),
	output: result.output,
	calls: result.calls.map((call) => ({ name: call.name, status: call.status })),
	...(result.during ? { during: result.during } : {}),
});

export async function run(cases, root, wasmPath) {
	const load = (path) => import(pathToFileURL(`${root}/pi-codemode/${path}`).href);
	const { serveCodemodeRemote, RemoteCodemodeSandbox } = await load("runtime/remote.js");
	const { InlineCodemodeSandbox, DEFAULT_INTERRUPT_BUDGET, INLINE_MAX_STACK_SIZE } = await load("runtime/inline.js");
	const { loadQuickJSWasm } = await load("wasm.js");
	const { PRELUDE_SOURCE } = await load("runtime/prelude-source.js");
	const wasm = loadQuickJSWasm(wasmPath);

	async function serve(spec) {
		const frames = [];
		const seqOf = new Map();
		let seq = 0;
		let held;
		const exchange = (messages) => {
			const frame = { messages: clone(messages), reply: "none" };
			frames.push(frame);
			const call = messages.find((message) => message.type === "call");
			if (!call) {
				frame.reply = null;
				return Promise.resolve(null);
			}
			const kind = (spec.replies ?? {})[call.name] ?? "echo";
			if (kind === "reject") {
				frame.reply = "rejected";
				return Promise.reject(new Error("the exchange failed"));
			}
			const extra = {};
			if (spec.data.deterministic) {
				seqOf.set(call.id, ++seq);
				extra.seq = seq;
				extra.now = spec.data.deterministic.startedAt + seq * 1000;
				// A client that breaks the protocol: the VM's answer is part of what Pi does.
				if (spec.shape === "badSeq") extra.seq = 0;
				if (spec.shape === "dupSeq") extra.seq = 1;
				if (spec.shape === "dupEarly") extra.seq = 2;
				if (spec.shape === "omitSeq") delete extra.seq;
				if (spec.shape === "omitNow") delete extra.now;
				if (spec.shape === "pastNow") extra.now = spec.data.deterministic.startedAt - seq * 5000;
				if (kind === "fail" && spec.properties) extra.properties = JSON.stringify(spec.properties);
			}
			const reply = replyFor(kind, call, extra);
			if (spec.reverse) {
				// Hold the first call's answer until the second call arrives, then answer the second first.
				if (held === undefined) {
					return new Promise((resolve) => {
						held = () => {
							frame.reply = clone(reply);
							resolve(reply);
						};
					});
				}
				const first = held;
				frame.reply = clone(reply);
				return new Promise((resolve) => {
					resolve(reply);
					queueMicrotask(first);
				});
			}
			frame.reply = clone(reply);
			return Promise.resolve(reply);
		};
		const request = { data: spec.data, ...(spec.interruptBudget === undefined ? {} : { interruptBudget: spec.interruptBudget }) };
		const options = {
			wasm: spec.badWasm ? Promise.reject(new Error("no module")) : wasm,
			...(spec.maxInterruptBudget === undefined ? {} : { maxInterruptBudget: spec.maxInterruptBudget }),
			...(spec.maxMemoryLimitBytes === undefined ? {} : { maxMemoryLimitBytes: spec.maxMemoryLimitBytes }),
		};
		const settled = await within(serveCodemodeRemote(request, exchange, options));
		return { returned: settled !== TIMEOUT, frames };
	}

	function toolsOf(spec) {
		return (spec.tools ?? []).map((tool) => ({ name: tool.name, description: tool.description ?? "", execute: toolBehavior(tool.kind, tool.name) }));
	}
	function globalsOf(spec) {
		return (spec.globals ?? []).map((global) => ({ name: global.name, spread: global.spread === true, description: "", execute: toolBehavior(global.kind, global.name) }));
	}

	async function client(spec) {
		const requests = [];
		const frames = [];
		let sandbox;
		const remote = () => ({
			execute(request, exchange) {
				requests.push(clone(request));
				const recording = async (messages) => {
					const frame = { messages: clone(messages), reply: "none" };
					frames.push(frame);
					// The caller ends the execution while its call is pending: the exchange is answered with a stop.
					if (spec.closeOnCall && messages.some((message) => message.type === "call")) queueMicrotask(() => void sandbox.close());
					const reply = await exchange(messages);
					frame.reply = clone(reply);
					return reply;
				};
				return serveCodemodeRemote(request, recording, {
					wasm,
					...(spec.maxInterruptBudget === undefined ? {} : { maxInterruptBudget: spec.maxInterruptBudget }),
					...(spec.maxMemoryLimitBytes === undefined ? {} : { maxMemoryLimitBytes: spec.maxMemoryLimitBytes }),
				});
			},
		});
		sandbox = new RemoteCodemodeSandbox({
			remote,
			tools: toolsOf(spec),
			globals: globalsOf(spec),
			...(spec.timeoutMs === undefined ? {} : { timeoutMs: spec.timeoutMs }),
			...(spec.memoryLimitBytes === undefined ? {} : { memoryLimitBytes: spec.memoryLimitBytes }),
			...(spec.interruptBudget === undefined ? {} : { interruptBudget: spec.interruptBudget }),
		});
		try {
			const result = await within(sandbox.execute(spec.code, spec.store === undefined ? {} : { store: spec.store }));
			return { requests, frames, result: result === TIMEOUT ? "timeout" : resultJson(result) };
		} finally {
			await sandbox.close();
		}
	}

	async function inline(spec) {
		const sandbox = new InlineCodemodeSandbox({
			wasm,
			tools: toolsOf(spec),
			globals: globalsOf(spec),
			...(spec.timeoutMs === undefined ? {} : { timeoutMs: spec.timeoutMs }),
			...(spec.memoryLimitBytes === undefined ? {} : { memoryLimitBytes: spec.memoryLimitBytes }),
			...(spec.interruptBudget === undefined ? {} : { interruptBudget: spec.interruptBudget }),
		});
		try {
			const result = await within(sandbox.execute(spec.code, spec.store === undefined ? {} : { store: spec.store }));
			return { result: result === TIMEOUT ? "timeout" : resultJson(result) };
		} finally {
			await sandbox.close();
		}
	}

	async function start(spec) {
		const events = [];
		const attempts = [];
		const sandbox =
			spec.backend === "remote"
				? new RemoteCodemodeSandbox({
						remote: () => ({ execute: (request, exchange) => serveCodemodeRemote(request, exchange, { wasm }) }),
						...(spec.interruptBudget === undefined ? {} : { interruptBudget: spec.interruptBudget }),
					})
				: new InlineCodemodeSandbox({ wasm, ...(spec.interruptBudget === undefined ? {} : { interruptBudget: spec.interruptBudget }) });
		const state = { seq: 0, attempted: false };
		let started;
		const number = (value) => (value === "NaN" ? Number.NaN : value === "Infinity" ? Number.POSITIVE_INFINITY : value);
		const attempt = (a) => {
			try {
				started.deliver(a.seq, number(a.now), a.id, a.outcome);
				attempts.push("ok");
			} catch (error) {
				attempts.push(error instanceof Error ? error.message : String(error));
			}
		};
		try {
			started = sandbox.start(spec.code, {
				startedAt: spec.startedAt,
				seed: spec.seed,
				tools: spec.declTools,
				globals: spec.declGlobals,
				store: spec.store,
				onStarted: (fingerprint) => events.push({ started: fingerprint }),
				onIdle: (seq, issued) => events.push({ idle: [seq, issued] }),
				onCall: (call) => {
					events.push({ call: clone(call) });
					if (!state.attempted) {
						state.attempted = true;
						for (const a of spec.attempts ?? []) attempt(a);
					}
					if (spec.noAnswer) return;
					const kind = (spec.replies ?? {})[call.name] ?? "echo";
					const seq = ++state.seq;
					const outcome =
						kind === "fail"
							? { ok: false, message: `failing ${call.name}`, ...(spec.properties ? { properties: spec.properties } : {}) }
							: { ok: true, value: kind === "undef" ? undefined : kind === "number" ? 42 : { tool: call.name, args: call.args ?? null } };
					// Answered from the next turn, as a host that does its own work first would.
					queueMicrotask(() => {
						try {
							started.deliver(seq, spec.startedAt + seq * 1000, call.id, outcome);
						} catch (error) {
							attempts.push(`deliver: ${error instanceof Error ? error.message : String(error)}`);
						}
					});
				},
			});
			const result = await within(started.result);
			return { events, attempts, finished: started.finished, result: result === TIMEOUT ? "timeout" : resultJson(result) };
		} finally {
			await sandbox.close();
		}
	}

	// A VM that sends what the case lists and records what the host answers: the host's reading of what a VM reports, with the
	// messages a real VM cannot be made to send.
	async function bridge(spec) {
		const { CodemodeSandboxBase } = await load("runtime/execution.js");
		const posted = [];
		let notify = () => undefined;
		const launcher = (data, events) => {
			queueMicrotask(async () => {
				for (const step of spec.steps) {
					if (step.send !== undefined) {
						if (step.send.type === "failure") events.failure(clone(step.send.error));
						else events.message(clone(step.send));
					} else {
						while (posted.length < step.wait) await new Promise((resolve) => (notify = resolve));
					}
				}
			});
			return {
				post: (message) => {
					posted.push(clone(message));
					notify();
				},
				stop: async () => undefined,
			};
		};
		const sandbox = new CodemodeSandboxBase({ tools: toolsOf(spec), globals: globalsOf(spec) }, launcher);
		try {
			const run = spec.control
				? sandbox.start(spec.code, { startedAt: 0, seed: 0, tools: spec.declTools, globals: spec.declGlobals, onCall() {} }).result
				: sandbox.execute(spec.code);
			const result = await within(run);
			// The tools answer from goroutines on the other side: the order of the answers is not a contract, their content is.
			posted.sort((a, b) => a.id - b.id);
			return { posted, result: result === TIMEOUT ? "timeout" : resultJson(result) };
		} finally {
			await sandbox.close();
		}
	}

	// A remote that sends what the case lists, one exchange after another, and records how the client answers each.
	async function remote(spec) {
		const replies = [];
		const sandbox = new RemoteCodemodeSandbox({
			tools: toolsOf(spec),
			globals: globalsOf(spec),
			remote: () => ({
				async execute(request, exchange) {
					for (const entry of spec.exchanges ?? []) replies.push(clone(await exchange(clone(entry.messages))) ?? null);
					if (spec.throws) throw new Error("remote exploded");
				},
			}),
		});
		try {
			const result = await within(sandbox.execute(spec.code));
			return { replies, result: result === TIMEOUT ? "timeout" : resultJson(result) };
		} finally {
			await sandbox.close();
		}
	}

	async function validate(spec) {
		const message = (fn) => {
			try {
				fn();
				return null;
			} catch (error) {
				return error instanceof Error ? error.message : String(error);
			}
		};
		const record = {};
		record.globals = message(
			() => new InlineCodemodeSandbox({ wasm, globals: (spec.globalNames ?? []).map((name) => ({ name, execute: async () => undefined })) }),
		);
		const plain = new InlineCodemodeSandbox({ wasm });
		const toolName = spec.toolName ?? "echo";
		const echo = { name: toolName, execute: async () => undefined };
		record.register = message(() => plain.registerTool(echo));
		record.registerAgain = message(() => plain.registerTool(echo));
		record.unregister = [plain.unregisterTool(toolName), plain.unregisterTool(toolName)];
		record.reRegister = message(() => plain.registerTool(echo));
		let started;
		record.start = message(() => {
			started = plain.start("return 1", { startedAt: spec.startedAt, seed: spec.seed, tools: spec.declTools, globals: spec.declGlobals, onCall() {} });
		});
		if (started) await started.result;
		await plain.close();
		return record;
	}

	const out = [];
	for (const spec of cases) {
		const modes = { serve, client, inline, start, validate, bridge, remote };
		out.push(await modes[spec.mode](spec));
	}
	return { cases: out, prelude: PRELUDE_SOURCE, constants: { defaultInterruptBudget: DEFAULT_INTERRUPT_BUDGET, inlineMaxStackSize: INLINE_MAX_STACK_SIZE } };
}
