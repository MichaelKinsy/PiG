// Run Pi's withCancel / withAbortSignal (packages/chord/src/context/index.ts, types stripped) over scenarios from stdin and answer, for
// each, the listeners in the order they ran, with the abort state of every named context at that moment and the reason.
// Scenario: {name, ops: [op...]}; op is one of
//   {op:"cancel", name, parent}              withCancel(parent): a new context with its own cancel
//   {op:"signal", name, parent, signal}      withAbortSignal(signal's abort signal, parent)
//   {op:"listen", name, on}                  context.abortSignal.addEventListener("abort", ..., {once: true}), as awaitWithContext does
//   {op:"abort", name, reason}               the cancel of context `name`, with new Error(reason)
// "root" is BACKGROUND_CONTEXT. Answer: {scenarios: [{name, log: [{listener, aborted: {name: bool}, reason}]}]}.
// usage: node abort_order_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const { BACKGROUND_CONTEXT, withAbortSignal, withCancel } = await import(pathToFileURL(`${packages}chord/src/context/index.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const scenarios = JSON.parse(input);
const answers = scenarios.map(({ name, ops }) => {
	const contexts = { root: BACKGROUND_CONTEXT };
	const cancels = {};
	const log = [];
	const snapshot = () => Object.fromEntries(Object.entries(contexts).filter(([key]) => key !== "root").map(([key, context]) => [key, context.abortSignal?.aborted ?? false]));
	for (const op of ops) {
		if (op.op === "cancel") {
			const { context, cancel } = withCancel(contexts[op.parent]);
			contexts[op.name] = context;
			cancels[op.name] = cancel;
		} else if (op.op === "signal") {
			contexts[op.name] = withAbortSignal(contexts[op.signal].abortSignal, contexts[op.parent]);
		} else if (op.op === "listen") {
			const signal = contexts[op.on].abortSignal;
			signal.addEventListener("abort", () => log.push({ listener: op.name, aborted: snapshot(), reason: String(signal.reason?.message) }), { once: true });
		} else if (op.op === "abort") {
			cancels[op.name](new Error(op.reason));
		}
	}
	return { name, log };
});
process.stdout.write(JSON.stringify({ scenarios: answers }));
