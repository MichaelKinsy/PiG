// Run Pi's packages/evals/src/plan.ts over probes from stdin: {op: "parse", value} or {op: "plan", cases, model, runs} -> {ok} | {err: "Name: message"}.
// usage: node plan.mjs <path to packages/evals/src/plan.ts>
import { pathToFileURL } from "node:url";
const { parseDiscoveredCases, createTaskPlan } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
	try {
		if (probe.op === "parse") return { ok: parseDiscoveredCases(probe.value) };
		return { ok: createTaskPlan(probe.cases, probe.model, probe.runs) };
	} catch (error) {
		return { err: `${error.name}: ${error.message}` };
	}
});
process.stdout.write(JSON.stringify(results));
