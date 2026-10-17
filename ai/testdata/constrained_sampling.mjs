// Run Pi's constrained-sampling.ts (imported as-is, types stripped) over probes from stdin.
// usage: node constrained_sampling.mjs <constrained-sampling.ts>
// {op: "strict"|"resolveStrict"|"grammar"|"input"|"delta", tool, supports, check, args, steps}. Results: {ok: value} or {err: message}; undefined is {ok: null, undef: true}.
import { pathToFileURL } from "node:url";
const sampling = await import(pathToFileURL(process.argv[2]).href);
const check = (key) => key === "format" || key === "pattern";
function settle(run) {
	try {
		const value = run();
		return value === undefined ? { undef: true } : { ok: value };
	} catch (error) {
		return { err: error instanceof Error ? error.message : String(error) };
	}
}
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			switch (p.op) {
				case "strict":
					return settle(() => sampling.getJsonSchemaToolParameters(p.tool, true));
				case "resolveStrict":
					return settle(() => sampling.resolveJsonSchemaStrictSampling(p.tool, p.supports, p.check ? check : undefined));
				case "grammar":
					return settle(() => sampling.resolveGrammarConstrainedSampling(p.tool, p.supports));
				case "input":
					return settle(() => sampling.getGrammarToolInput(p.tool.name, p.args, p.property));
				default: {
					const buffer = { input: "", started: false, closed: false };
					return { steps: p.steps.map((step) => settle(() => sampling.appendGrammarToolInputJsonDelta(buffer, p.property, step.next, step.close))) };
				}
			}
		}),
	),
);
