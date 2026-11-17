// Run Pi's withCancel / withAbortSignal / withoutAbortSignal (packages/chord/src/context/index.ts, types stripped) over scenarios
// from stdin and answer, for each, the abort listeners that ran, in the order they ran, with the reason each heard.
// Scenario: {name, ops: [op...]}; op is one of
//   {op:"cancel", name, parent}                  withCancel(parent): a new context with its own cancel
//   {op:"signal", name, parent, signal}          withAbortSignal(signal's abort signal, parent)
//   {op:"without", name, parent}                 withoutAbortSignal(parent)
//   {op:"listen", name, on, abort?: {name, reason}}
//        a listener of context `on`, as awaitWithContext adds it (context/index.ts:138-153): a context with no abort signal never
//        runs it, a signal aborted already runs it at once, and a live one runs it inside the abort, once; with `abort` the listener
//        cancels context abort.name again with abort.reason when it runs
//   {op:"abort", name, reason}                   the cancel of context `name`, with new Error(reason)
// "root" is BACKGROUND_CONTEXT. Answer: {scenarios: [{name, log: [{listener, reason}]}]}.
// usage: node abort_cause_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const { BACKGROUND_CONTEXT, withAbortSignal, withCancel, withoutAbortSignal } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const scenarios = JSON.parse(input);
const answers = scenarios.map(({ name, ops }) => {
	const contexts = { root: BACKGROUND_CONTEXT };
	const cancels = {};
	const log = [];
	for (const op of ops) {
		if (op.op === "cancel") {
			const { context, cancel } = withCancel(contexts[op.parent]);
			contexts[op.name] = context;
			cancels[op.name] = cancel;
		} else if (op.op === "signal") {
			contexts[op.name] = withAbortSignal(contexts[op.signal].abortSignal, contexts[op.parent]);
		} else if (op.op === "without") {
			contexts[op.name] = withoutAbortSignal(contexts[op.parent]);
		} else if (op.op === "listen") {
			const signal = contexts[op.on].abortSignal;
			if (signal === undefined) continue;
			const run = () => {
				log.push({ listener: op.name, reason: String(signal.reason?.message) });
				if (op.abort) cancels[op.abort.name](new Error(op.abort.reason));
			};
			if (signal.aborted) run();
			else signal.addEventListener("abort", run, { once: true });
		} else if (op.op === "abort") {
			cancels[op.name](new Error(op.reason));
		}
	}
	return { name, log };
});
process.stdout.write(JSON.stringify({ scenarios: answers }));
