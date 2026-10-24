// Run Pi's real durable Harness with its code mode tool (packages/durable/src/codemode/codemode.ts) and its real
// CodemodeSandbox, over a faux model, a controlled clock and controlled storage, through the restart and replay sequences
// each scenario names, and print what Pi observed: the call's result and details, the stored documents, the tools' run
// counts, the entries and the error texts.
// The sources are imported as-is with types stripped; the packages they import come from the installed pi-durable.
// A scenario is {name, code, globals?: [name], namespaces?, steps: [step]}; the step ops are listed in the switch below, and
// durable/codemode/call_restart_pi_test.go runs the same steps against PiG.
// usage: node codemode_restart_oracle.mjs <packages dir>   stdin: the scenarios (JSON)   (PI_INTEROP_DIR: where pi-durable is installed)
import { realpathSync } from "node:fs";
import module from "node:module";
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
// The mirror is reached through a symlink; modules report their real path.
const sourceRoot = pathToFileURL(realpathSync(packages)).href;
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
const parent = pathToFileURL(`${interop}node_modules/@earendil-works/pi-durable/dist/index.js`).href;
const durableIndex = pathToFileURL(`${realpathSync(packages)}/durable/src/index.ts`).href;
module.registerHooks({
	resolve(specifier, context, next) {
		const fromPi = context.parentURL?.startsWith(sourceRoot) || context.parentURL === import.meta.url;
		// One module graph: the package's own entry is its source, whoever imports it.
		if (fromPi && specifier === "@earendil-works/pi-durable") return next(durableIndex, context);
		if (fromPi && !/^(\.|\/|node:|file:|data:)/.test(specifier)) {
			return next(specifier, { ...context, parentURL: parent });
		}
		return next(specifier, context);
	},
});
const load = (path) => import(pathToFileURL(`${packages}${path}`).href);
const { CodemodeSandbox } = await import("@earendil-works/pi-codemode");
const { fauxAssistantMessage, fauxText, fauxToolCall, Type } = await import("@earendil-works/pi-ai");
const { defineTool, ToolResultEntry, ToolTask } = await import("@earendil-works/pi-durable");
const codemode = await load("durable/src/codemode/codemode.ts");
const { chatSetup, openChat, allEntries } = await load("durable/test/chat-support.ts");
const { addTool } = await load("durable/test/harness-support.ts");
const { ControlledStorage, context } = await load("durable/test/session-support.ts");
const { aborted, deferred, eventually } = await load("durable/test/task-support.ts");

/**
 * Storage whose commits a scenario controls: after `queue()`, each commit waits in order until `release()` lets the oldest
 * go (and fail, when given a message); `entered` counts the commits that reached storage since.
 */
class QueuedStorage extends ControlledStorage {
	queueing = false;
	marked = false;
	entered = 0;
	/** For each commit since the mark that appended journal entries, how many. */
	pushes = [];
	waiting = [];
	failure;
	queue() {
		this.queueing = true;
		this.mark();
	}
	mark() {
		this.marked = true;
		this.entered = 0;
		this.pushes = [];
	}
	release(message) {
		const next = this.waiting.shift();
		if (next === undefined) throw new Error("no commit is waiting");
		next.failure = message === undefined ? undefined : new Error(message);
		next.gate.resolve();
	}
	unqueue() {
		this.queueing = false;
		while (this.waiting.length > 0) this.release();
	}
	failNext(message) {
		this.failure = new Error(message);
	}
	async commit(writes, adopt, commitContext) {
		this.entered++;
		if (this.marked) {
			let appended = 0;
			for (const write of writes) {
				if (write.type !== "document.change") continue;
				for (const [verb, path, , , items] of write.content.ops ?? []) {
					if (verb === "p" && path.length === 1 && path[0] === "entries") appended += items.length;
				}
			}
			if (appended > 0) this.pushes.push(appended);
		}
		if (this.queueing) {
			const entry = { gate: deferred(), failure: undefined };
			this.waiting.push(entry);
			await entry.gate.promise;
			if (entry.failure !== undefined) throw entry.failure;
		}
		if (this.failure !== undefined) {
			const failure = this.failure;
			this.failure = undefined;
			throw failure;
		}
		return super.commit(writes, adopt, commitContext);
	}
}

const Parameters = Type.Object({ text: Type.Optional(Type.String()), id: Type.Optional(Type.Number()) });
const tool = (name, execute, replay = "unsafe") => defineTool({ name, description: name, parameters: Parameters, execute, replay });
const DONE = () => fauxAssistantMessage([fauxText("done")]);
const script = (code) => fauxAssistantMessage([fauxToolCall("codemode", { code }, { id: "s1" })], { stopReason: "toolUse" });
const BASE_CLOCK = 1_700_000_000_000;

/** Walk `path` through a document draft to the object that holds its last key. */
function parentOf(root, path) {
	let node = root;
	for (const key of path.slice(0, -1)) node = node[key];
	return [node, path[path.length - 1]];
}

/** The call rows of a result's details without their durations, which are real time. */
function withoutDurations(details) {
	for (const call of details.calls ?? []) delete call.durationMs;
	return details;
}

/** The stored document with what differs between two runs of one scenario (the seed, the VM's fingerprint) removed. */
function normalized(doc) {
	if (doc === undefined) return null;
	const copy = JSON.parse(JSON.stringify(doc));
	if (copy.start !== undefined) {
		delete copy.start.seed;
		delete copy.start.fingerprint;
	}
	return copy;
}

async function runScenario(scenario) {
	const setup = chatSetup();
	let clock = BASE_CLOCK;
	setup.now = () => clock;
	const sandbox = new CodemodeSandbox({ memoryLimitBytes: 64 * 1024 * 1024 });
	// What the VM reports and when the call may stop it, held on request: `events` queues what the VM reports, in order, and
	// `abort` lets the call's request to stop the VM wait.
	const vm = { starts: 0, holdEvents: false, queued: [], holdAbort: false, heldAborts: [], abortRequested: deferred(), ended: deferred() };
	const start = sandbox.start.bind(sandbox);
	sandbox.start = (code, options) => {
		vm.starts++;
		const own = new AbortController();
		const stop = () => own.abort(options.signal.reason);
		const requested = () => {
			vm.abortRequested.resolve();
			if (vm.holdAbort) vm.heldAborts.push(stop);
			else stop();
		};
		if (options.signal.aborted) requested();
		else options.signal.addEventListener("abort", requested, { once: true });
		const forward = (name) => (...args) => {
			if (vm.holdEvents) vm.queued.push(() => options[name](...args));
			else options[name](...args);
		};
		const run = start(code, {
			...options,
			signal: own.signal,
			onStarted: forward("onStarted"),
			onCall: forward("onCall"),
			onIdle: forward("onIdle"),
		});
		void run.result.then(() => vm.ended.resolve());
		return run;
	};
	const gates = new Map();
	const once = (map, id) => {
		const found = map.get(id) ?? deferred();
		map.set(id, found);
		return found;
	};
	let counter = 0;
	let finishedGlobals = 0;
	const globalImplementations = {
		// Waits for the scenario to release its argument (the gate `slow` waits on), then returns it.
		gslow: async (args, _api, callContext) => {
			await Promise.race([once(gates, args).promise, aborted(callContext.abortSignal)]);
			finishedGlobals++;
			return args;
		},
		next: async () => ++counter,
		constant: async () => "c",
		boom: async () => {
			throw new Error("boom");
		},
		undef: async () => undefined,
	};
	const globals = (scenario.globals ?? []).map((name) => ({ name, execute: globalImplementations[name] }));
	const installed = new Map();
	const install = (name, added) => installed.set(name, added);
	addTool(setup.registry, codemode.createCodemodeTool({ sandbox, globals, namespaces: scenario.namespaces ?? [] }));
	const echo = new Map();
	addTool(
		setup.registry,
		tool("echo", async (args) => {
			echo.set(args.text ?? "", (echo.get(args.text ?? "") ?? 0) + 1);
			return { output: [{ type: "text", text: `echo ${args.text ?? ""}` }] };
		}),
	);
	const holds = { block: true, reached: deferred(), runs: 0 };
	addTool(
		setup.registry,
		tool(
			"hold",
			async (args, _api, callContext) => {
				holds.runs++;
				if (holds.block) {
					holds.reached.resolve();
					await aborted(callContext.abortSignal);
				}
				return { output: [{ type: "text", text: `held ${args.id ?? ""}` }] };
			},
			"safe",
		),
	);
	const pauses = { block: true, reached: deferred() };
	addTool(
		setup.registry,
		tool(
			"pause",
			async (_args, _api, callContext) => {
				if (pauses.block) {
					pauses.reached.resolve();
					await aborted(callContext.abortSignal);
				}
				return { output: [{ type: "text", text: "paused" }] };
			},
			"safe",
		),
	);
	addTool(
		setup.registry,
		tool("slow", async (args, _api, callContext) => {
			await Promise.race([once(gates, args.id ?? 0).promise, aborted(callContext.abortSignal)]);
			return { output: [{ type: "text", text: `slow ${args.id}` }] };
		}),
	);
	addTool(
		setup.registry,
		tool("fails", async () => ({ output: [{ type: "text", text: "permission denied" }], isError: true })),
	);
	// A tool with the name of a global, for scripts that call one where they called the other.
	install("next", addTool(setup.registry, tool("next", async () => ({ output: [{ type: "text", text: "tool next" }] }))));

	const storage = new QueuedStorage();
	let opened;
	let submission;
	let lastStatus = null;
	let closing;
	const editing = [];
	const observations = [];
	// A read goes through the Session's line, behind a commit held in storage, so the task is looked up once.
	let knownTask;
	const scriptTask = async () => {
		if (knownTask !== undefined) return knownTask;
		const tasks = (await opened.harness.commit((tx) => tx.scanTasks({ kind: ToolTask.definition.name }, 100), context)).items;
		knownTask = tasks.find((task) => task.input.kind === "model")?.id;
		return knownTask;
	};
	const document = async (token, owner) => {
		const found = await opened.harness.snapshot(token, owner, context);
		return found === undefined ? undefined : found;
	};
	const journal = async () => {
		const task = await scriptTask();
		return task === undefined ? undefined : document(codemode.CodemodeJournalDoc, task);
	};
	const nested = async () => {
		const tasks = (await opened.harness.commit((tx) => tx.scanTasks({ kind: ToolTask.definition.name }, 100), context)).items;
		return tasks.filter((task) => task.input.kind === "nested").length;
	};
	const observe = async (label, light) => {
		if (light) {
			// What a failed Session still lets the host see: the reports and the tools' run counts.
			observations.push({
				label,
				counts: { commits: storage.marked ? [...storage.pushes] : null, starts: vm.starts, echo: Object.fromEntries([...echo].sort()), holds: holds.runs, next: counter },
				reports: setup.reports.map((report) => (report instanceof Error ? report.message : String(report))),
				status: lastStatus,
			});
			return;
		}
		const entries = await allEntries(opened.root);
		const result = entries.findLast((entry) => ToolResultEntry.is(entry));
		const message = result?.model?.[0];
		const task = await scriptTask();
		const state = {
			label,
			result:
				message === undefined
					? null
					: {
							text: message.content.map((item) => (item.type === "text" ? item.text : "")).join(""),
							isError: message.isError,
							details: message.details === undefined ? null : withoutDurations(JSON.parse(JSON.stringify(message.details))),
						},
			entries: entries.map((entry) => {
				const model = entry.model?.[0];
				return model === undefined
					? "-"
					: `${model.role}:${typeof model.content === "string" ? model.content : model.content.map((item) => (item.type === "text" ? item.text : item.type)).join("")}`;
			}),
			run: task === undefined ? null : normalized(await document(codemode.CodemodeRunDoc, task)),
			journal: task === undefined ? null : normalized(await document(codemode.CodemodeJournalDoc, task)),
			store: normalized(await document(codemode.CodemodeStoreDoc, opened.root.id)),
			counts: { commits: storage.marked ? [...storage.pushes] : null, starts: vm.starts, echo: Object.fromEntries([...echo].sort()), holds: holds.runs, nested: await nested(), next: counter },
			reports: setup.reports.map((report) => (report instanceof Error ? report.message : String(report))),
			status: lastStatus,
		};
		observations.push(state);
	};
	for (const [position, step] of scenario.steps.entries()) {
		try {
			if (process.env.CDH_TRACE) console.error(position, step.op, step.what ?? "");
			switch (step.op) {
				case "start": {
					setup.faux.setResponses([script(step.code ?? scenario.code), DONE()]);
					opened ??= await openChat(storage, setup);
					knownTask = undefined;
					submission = await opened.root.submit({ type: "input", content: "go" }, context);
					break;
				}
				case "await": {
					switch (step.what) {
						case "hold":
							await holds.reached.promise;
							break;
						case "journal": {
							const want = step.entries;
							await eventually(async () => ((await journal())?.entries?.length ?? 0) >= want);
							break;
						}
						case "issuedAfter":
							await eventually(async () => (await journal())?.entries?.[step.index]?.issuedAfter === step.value);
							break;
						case "firstIssued":
							await eventually(async () => (await journal())?.firstIssued === step.value);
							break;
						case "commits":
							await eventually(() => storage.entered >= step.count);
							break;
						case "globals-finished":
							await eventually(() => finishedGlobals >= step.count);
							break;
						case "vm-ended":
							await vm.ended.promise;
							break;
						case "held-events":
							await eventually(() => vm.queued.length >= step.count);
							break;
						case "abort-requested":
							await vm.abortRequested.promise;
							break;
						case "nested":
							await eventually(async () => (await nested()) >= step.count);
							break;
						case "done":
							lastStatus = await submission.wait(context).then(
								(settled) => settled.status,
								(error) => `error: ${error instanceof Error ? error.message : error}`,
							);
							break;
						default:
							throw new Error(`unknown await ${step.what}`);
					}
					break;
				}
				case "close":
					await opened.harness.close(context);
					break;
				case "close-begin":
					closing = opened.harness.close(context);
					break;
				case "close-end":
					await closing;
					break;
				case "open": {
					storage.reopen();
					if (step.rearm) holds.reached = deferred();
					if (step.unblock) holds.block = false;
					opened = await openChat(storage, setup);
					if (step.submission) submission = await opened.harness.submission(submission.id, context);
					break;
				}
				case "resume":
					opened.harness.resume();
					submission = await opened.harness.submission(submission.id, context);
					break;
				case "unblock":
					holds.block = false;
					break;
				case "release":
					once(gates, step.id).resolve();
					break;
				case "clock":
					clock = step.ms === undefined ? clock + step.advance : step.ms;
					break;
				case "edit":
				case "edit-begin": {
					const task = await scriptTask();
					const committing = opened.harness.commit(async (tx) => {
						const draft =
							step.doc === "run"
								? await tx.doc(codemode.CodemodeRunDoc, task)
								: step.doc === "journal"
									? await tx.doc(codemode.CodemodeJournalDoc, task)
									: await tx.doc(codemode.CodemodeStoreDoc, opened.root.id);
						if (step.push) {
							let array = draft;
							for (const key of step.path) array = array[key];
							array.push(step.value);
							return;
						}
						if (step.length !== undefined) {
							let array = draft;
							for (const key of step.path) array = array[key];
							array.length = step.length;
							return;
						}
						const [node, key] = parentOf(draft, step.path);
						if (step.delete) delete node[key];
						else node[key] = step.value;
					}, context);
					if (step.op === "edit") await committing;
					else editing.push(committing);
					break;
				}
				case "edit-end":
					await Promise.all(editing.splice(0));
					break;
				case "mark-commits":
					storage.mark();
					break;
				case "queue-commits":
					storage.queue();
					break;
				case "release-commit":
					storage.release(step.message);
					break;
				case "unqueue-commits":
					storage.unqueue();
					break;
				case "fail-next-commit":
					storage.failNext(step.message);
					break;
				case "uninstall-tool":
				installed.get(step.tool).dispose();
				break;
			case "hold-events":
					vm.holdEvents = true;
					break;
				case "release-events":
					vm.holdEvents = false;
					for (const event of vm.queued.splice(0)) event();
					break;
				case "hold-abort":
					vm.holdAbort = true;
					break;
				case "release-abort":
					vm.holdAbort = false;
					for (const stop of vm.heldAborts.splice(0)) stop();
					break;
				case "observe":
					await observe(step.label ?? "", step.light === true);
					break;
				default:
					throw new Error(`unknown op ${step.op}`);
			}
		} catch (error) {
			throw new Error(`step ${position} ${JSON.stringify(step)}: ${error instanceof Error ? error.message : error}`);
		}
	}
	await opened.harness.close(context).catch(() => {});
	return observations;
}

// A scenario that waits for what never comes fails the run instead of hanging it.
setTimeout(() => {
	console.error("the scenarios did not finish in two minutes");
	process.exit(3);
}, 120_000).unref();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const scenario of JSON.parse(input)) {
	try {
		out.push({ ok: await runScenario(scenario) });
	} catch (error) {
		out.push({ err: error instanceof Error ? error.message : String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
process.exit(0);
