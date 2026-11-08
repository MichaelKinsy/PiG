// Extract the cases of Pi's packages/ai/test/validation-probes.test.ts into JSON, so the Go port runs the same inputs against the same expected outcomes.
// usage: node --disable-warning=ExperimentalWarning validation_probes_corpus.mjs <repo root> > validation-probes.json
// Pi's test file runs unchanged: `vitest` and the validation module are replaced by recorders, and TypeBox (the oracle install in internal/typebox/testdata/npm)
// builds the schemas, which are recorded as the JSON text a tool declaration carries. Each row is one `expect(outcome(...)).toBe(expected)` of the file.
// Regenerate with the mirror at the Pi commit the port tracks (.upstream/current at c23b9ec1 or later).
import module from "node:module";
import { pathToFileURL } from "node:url";

const root = process.argv[2];
const typeboxDir = `${root}/internal/typebox/testdata/npm/`;
const mirror = `${root}/.upstream/current/`;
const rows = [];
const state = { path: [], last: undefined };
globalThis.__probeRecorder = {
	describe(name, body) {
		state.path.push(name);
		body();
		state.path.pop();
	},
	it(name, body) {
		state.path.push(name);
		state.last = undefined;
		body();
		state.path.pop();
	},
	validate(tool, call) {
		state.last = { name: tool.name, schema: JSON.stringify(tool.parameters), args: JSON.stringify(call.arguments) };
		return {};
	},
	expect(expected) {
		if (!state.last) throw new Error(`expectation without a validation call in ${state.path.join(" > ")}`);
		rows.push({ test: state.path.join(" > "), ...state.last, expected });
		state.last = undefined;
	},
};

const virtual = {
	"virtual:vitest": `export const describe = (n, f) => globalThis.__probeRecorder.describe(n, f);
export const it = (n, f) => globalThis.__probeRecorder.it(n, f);
export const expect = (actual) => ({ toBe: (expected) => globalThis.__probeRecorder.expect(expected) });`,
	"virtual:validation": "export const validateToolArguments = (tool, call) => globalThis.__probeRecorder.validate(tool, call);",
};
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier === "vitest") return { url: "virtual:vitest", shortCircuit: true };
		if (specifier.endsWith("/utils/validation.ts")) return { url: "virtual:validation", shortCircuit: true };
		if (specifier === "typebox" || specifier.startsWith("typebox/")) {
			return next(specifier, { ...context, parentURL: pathToFileURL(typeboxDir + "x.mjs").href });
		}
		return next(specifier, context);
	},
	load(url, context, next) {
		if (url in virtual) return { format: "module", source: virtual[url], shortCircuit: true };
		return next(url, context);
	},
});

await import(pathToFileURL(`${mirror}packages/ai/test/validation-probes.test.ts`).href);
process.stdout.write(`${JSON.stringify(rows, null, 1)}\n`);
