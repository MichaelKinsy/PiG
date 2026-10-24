// Run the private helpers of Pi's packages/durable/src/codemode/codemode.ts. The file is Pi's own, imported with types
// stripped; the only change is a line appended on load that exports the helpers the module keeps private.
// Op: {op: "cut", text, max} | {op: "textOf", value?} | {op: "outcome", name, structured, result}
//   | {op: "errorResult", message} | {op: "asToolCall", call} | {op: "countImages", value?}
//   | {op: "describe", call} | {op: "sameJson", a?, b?}
// Values are JSON text so that undefined and null stay apart: a missing key is undefined.
// stdout: per op {ok} or {err}.
// usage: node codemode_helpers_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-durable)
import module from "node:module";
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
const parent = pathToFileURL(`${interop}node_modules/@earendil-works/pi-durable/dist/index.js`).href;
const target = pathToFileURL(`${packages}durable/src/codemode/codemode.ts`).href;
module.registerHooks({
	resolve(specifier, context, next) {
		if (!/^(\.|\/|node:|file:|data:)/.test(specifier)) {
			return next(specifier, { ...context, parentURL: parent });
		}
		return next(specifier, context);
	},
	load(url, context, next) {
		const loaded = next(url, context);
		// The mirror is reached through a symlink, so the URL is the real path: match its tail.
		if (!url.endsWith("/durable/src/codemode/codemode.ts")) return loaded;
		const source = Buffer.from(loaded.source).toString("utf8");
		return {
			...loaded,
			source: `${source}\nexport { cut, outcomeOf, textOf, errorResult, asToolCall, countImages, describe, sameJson };\n`,
		};
	},
});
const codemode = await import(target);

const parse = (text) => (text === undefined || text === null ? undefined : JSON.parse(text));
const callOf = (raw) => ({ id: raw.id, target: raw.target, name: raw.name, ...(raw.args === undefined || raw.args === null ? {} : { args: parse(raw.args) }) });
const show = (value) => (value === undefined ? null : JSON.stringify(value));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		switch (op.op) {
			case "cut":
				out.push({ ok: codemode.cut(op.text, op.max) });
				break;
			case "textOf":
				out.push({ ok: codemode.textOf(parse(op.value)) });
				break;
			case "outcome": {
				const result = {
					isError: op.result.isError,
					diagnostics: op.result.diagnostics,
					...(op.result.structuredOutput === undefined || op.result.structuredOutput === null ? {} : { structuredOutput: parse(op.result.structuredOutput) }),
				};
				const outcome = codemode.outcomeOf(op.name, op.structured, result);
				out.push({ ok: { ok: outcome.ok, value: show(outcome.value), message: outcome.message ?? null, properties: show(outcome.properties) } });
				break;
			}
			case "errorResult":
				out.push({ ok: codemode.errorResult(op.message) });
				break;
			case "asToolCall": {
				const call = codemode.asToolCall(callOf(op.call));
				out.push({ ok: { id: call.id, target: call.target, name: call.name, args: show(call.args) } });
				break;
			}
			case "countImages":
				out.push({ ok: codemode.countImages(parse(op.value)) });
				break;
			case "describe":
				out.push({ ok: codemode.describe(callOf(op.call)) });
				break;
			case "sameJson":
				out.push({ ok: codemode.sameJson(parse(op.a), parse(op.b)) });
				break;
			default:
				throw new Error(`unknown op ${op.op}`);
		}
	} catch (error) {
		out.push({ err: error instanceof Error ? error.message : String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
