// Run typebox 1.3.27 Value.Check and Value.Errors over cases from stdin: each case is {schema: "<Type.* expression using R, the named refinements>", value}.
// The answer per case is the schema in the host's wire form (a refinement is {"\u0000":"f","i":N}, N the index into `names`), what Check and Errors answer, and every call
// of a refinement each made, in order: [name, "check"|"error", value].
// usage: node refine_oracle.mjs <repo root> <typebox version>
import module from "node:module";
import { pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
const root = process.argv[2];
const typeboxDir = `${root}/internal/typebox/testdata/npm/`;
const version = JSON.parse(readFileSync(typeboxDir + "node_modules/typebox/package.json", "utf8")).version;
if (version !== process.argv[3]) throw new Error(`Wrong typebox ${version}`);
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier === "typebox" || specifier.startsWith("typebox/")) return next(specifier, { ...context, parentURL: pathToFileURL(typeboxDir + "x.mjs").href });
		return next(specifier, context);
	},
});
const { Type } = await import("typebox");
const { Value } = await import("typebox/value");

let log = [];
const names = ["pos", "safe", "even", "nonEmpty", "ok", "bad", "sumLt10", "ab"];
const semantics = {
	pos: (v) => typeof v === "number" && v > 0,
	safe: (v) => v === "safe",
	even: (v) => (Array.isArray(v) ? v.length % 2 === 0 : v % 2 === 0),
	nonEmpty: (v) => (Array.isArray(v) ? v.length > 0 : Object.keys(v).length > 0),
	ok: () => true,
	bad: () => false,
	sumLt10: (v) => v.reduce((a, b) => a + b, 0) < 10,
	ab: (v) => v.a < v.b,
};
const R = {};
const identity = new Map();
for (const name of names) {
	const check = (value) => (log.push([name, "check", structuredClone(value)]), semantics[name](value));
	const error = (value) => (log.push([name, "error", structuredClone(value)]), `${name} rejected ${JSON.stringify(value)}`);
	R[name] = [check, error];
	identity.set(check, names.indexOf(name));
}
const refine = (schema, name) => Type.Refine(schema, ...R[name]);

function wire(value) {
	if (Array.isArray(value)) return value.map(wire);
	if (value === null || typeof value !== "object") return value;
	const hidden = {};
	const enumerable = {};
	for (const key of Object.getOwnPropertyNames(value)) {
		const member = value[key];
		const target = Object.prototype.propertyIsEnumerable.call(value, key) ? enumerable : hidden;
		if (key === "~refine") target[key] = member.map((entry) => ({ "\u0000": "f", i: identity.get(entry.check) }));
		else target[key] = wire(member);
	}
	if (Object.keys(hidden).length === 0 && !Object.keys(enumerable).some((key) => key.startsWith("\u0000"))) return enumerable;
	return { "\u0000": "o", h: hidden, e: enumerable };
}

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const answers = JSON.parse(input).map(({ schema: source, value }) => {
	const schema = new Function("Type", "R", "refine", `return ${source}`)(Type, R, refine);
	log = [];
	const check = Value.Check(schema, structuredClone(value));
	const checkCalls = log;
	log = [];
	const errors = [...Value.Errors(schema, structuredClone(value))].map((error) => ({ keyword: error.keyword, schemaPath: error.schemaPath, instancePath: error.instancePath, params: error.params, message: error.message }));
	const errorCalls = log;
	return { wire: wire(schema), check, checkCalls, errors, errorCalls };
});
process.stdout.write(JSON.stringify(answers));
