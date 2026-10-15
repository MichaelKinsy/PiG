// Run Pi's packages/evals/src/report.ts summarizeEvalObservations and formatEvalComparisonReport over probes from stdin:
// {digest, expected, observations} -> {report, text}. report.ts imports @vitest-evals/core, which is not installed and which these two functions
// do not use, so a resolve/load hook replaces it with an empty module.
// usage: node report.mjs <path to packages/evals/src/report.ts>
import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";
registerHooks({
	resolve(specifier, context, next) {
		if (specifier.startsWith("@vitest-evals/core")) return { url: `stub:${specifier}`, shortCircuit: true };
		return next(specifier, context);
	},
	load(url, context, next) {
		if (url.startsWith("stub:")) {
			return { format: "module", source: "export const readReportWorkspace = undefined; export const readVitestJsonReportFile = undefined;", shortCircuit: true };
		}
		return next(url, context);
	},
});
const { summarizeEvalObservations, formatEvalComparisonReport } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
	try {
		const report = summarizeEvalObservations(probe.digest, probe.expected, probe.observations);
		return { report, text: formatEvalComparisonReport(report) };
	} catch (error) {
		return { err: `${error.name}: ${error.message}` };
	}
});
process.stdout.write(JSON.stringify(results));
