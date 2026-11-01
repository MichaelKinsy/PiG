// Generates schema/testdata/coerce-golden.json: for a seeded corpus of schemas and loosely typed values, the result of Pi's
// coerce() and parse() (packages/schema at Pi commit b556cfe5: `compile(schema, { formats, coerce: true })`), with the member order
// of every object, whether coerce() returned its input itself, and whether parse() succeeded and with which value (the errors of a
// failed parse are the explanation's, not the coercion's, and are not recorded). TestCoerceMatchesPi replays it.
// Run through Node 24 against a Pi export of packages/schema: node gen-coerce-golden.mjs <pi>/packages/schema <out.json> [<pi-commit>]
import { writeFileSync } from "node:fs";

const root = process.argv[2];
const commit = process.argv[4] ?? "b556cfe5";
const { compile } = await import(`${root}/src/index.ts`);
const { formats } = await import(`${root}/src/formats/index.ts`);

// JSON text of a value with -0 kept; members in own-key order.
function enc(value) {
	if (value === null) return "null";
	switch (typeof value) {
		case "number":
			if (Object.is(value, -0)) return "-0";
			if (!Number.isFinite(value)) throw new Error(`non-finite ${value}`);
			return JSON.stringify(value);
		case "string":
		case "boolean":
			return JSON.stringify(value);
		case "undefined":
			throw new Error("undefined");
	}
	if (Array.isArray(value)) return `[${value.map(enc).join(",")}]`;
	return `{${Object.keys(value)
		.map((key) => `${JSON.stringify(key)}:${enc(value[key])}`)
		.join(",")}}`;
}

function mulberry32(seed) {
	let a = seed >>> 0;
	return () => {
		a = (a + 0x6d2b79f5) >>> 0;
		let t = a;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const rnd = mulberry32(20260501);
const chance = (p) => rnd() < p;
const pick = (list) => list[Math.floor(rnd() * list.length)];
const int = (n) => Math.floor(rnd() * n);

const TYPES = ["string", "number", "integer", "boolean", "null"];
const NAMES = ["a", "b", "c", "n_1", "n_2", "x", "10", "2", "path", "offset"];
const LITERALS = ["a", "b", "1", "2", 1, 2, 0, true, false, "true", "null", 1.5, "hello"];
const EDGE_STRINGS = [
	"", " ", "0", "1", "-1", "1.5", "-0", "0x1F", "0X1f", "0b11", "0o7", "1e3", "1E-2", " 1 ", "\u00a01", "\ufeff2", "\u20281\u2029",
	"1_000", "Infinity", "-Infinity", "+Infinity", "NaN", "true", "TRUE", "True", "false", "FALSE", "null", "NULL", "undefined", "UNDEFİNED",
	"123n", "-0n", "9007199254740991", "9007199254740992", "9007199254740991n", "-9007199254740991n", "12.5", "-12.50", "1.", ".5",
	"+5", "00", "0.0", "1e999", "abc", "\ud800", "İ", "ǅ", "1,5", "١٢", "0.1e1", "-.5", "- 1", "12n ", "5.0n", "-0.7",
];
const EDGE_NUMBERS = [0, -0, 1, -1, 2, 1.5, -2.5, 0.1, 1e21, 5e-324, 9007199254740993, 1e-7, 42, 3.14];

// ------------------------------------------------------------------------------------------------------------------
// Schemas
// ------------------------------------------------------------------------------------------------------------------

function leaf(ctx) {
	switch (int(9)) {
		case 0:
		case 1:
		case 2:
			return { type: pick(TYPES) };
		case 3:
			return { type: [pick(TYPES), pick(TYPES)] };
		case 4:
			return { enum: [pick(LITERALS), pick(LITERALS), pick(LITERALS)] };
		case 5:
			return { const: pick(LITERALS) };
		case 6:
			return chance(0.5) ? {} : chance(0.5) ? true : false;
		case 7:
			return { type: ["integer", "null"] };
		default:
			return { type: pick(["string", "number", "integer"]), minimum: 1, minLength: 1 };
	}
}

function properties(ctx, depth) {
	const props = {};
	for (let i = 0, n = 1 + int(3); i < n; i++) props[pick(NAMES)] = gen(ctx, depth - 1);
	return props;
}

function objectSchema(ctx, depth) {
	const props = properties(ctx, depth);
	const schema = { type: "object", properties: props };
	const names = Object.keys(props);
	if (chance(0.6)) schema.required = names.filter(() => chance(0.5));
	if (chance(0.25)) schema.patternProperties = { [pick(["^n_", "^x", "^[0-9]+$", "a|b"])]: gen(ctx, depth - 1) };
	if (chance(0.5)) schema.additionalProperties = chance(0.5) ? false : gen(ctx, depth - 1);
	return schema;
}

function gen(ctx, depth) {
	if (depth <= 0) return leaf(ctx);
	switch (int(16)) {
		case 0:
		case 1:
		case 2:
			return leaf(ctx);
		case 3:
		case 4:
			return { type: "array", items: gen(ctx, depth - 1), ...(chance(0.2) ? { minItems: 1 } : {}) };
		case 5:
			return ctx.draft === "7"
				? { type: "array", items: [gen(ctx, depth - 1), gen(ctx, depth - 1)], ...(chance(0.5) ? { additionalItems: gen(ctx, depth - 1) } : {}) }
				: { type: "array", prefixItems: [gen(ctx, depth - 1), gen(ctx, depth - 1)], items: chance(0.5) ? false : gen(ctx, depth - 1) };
		case 6:
		case 7:
		case 8:
			return objectSchema(ctx, depth);
		case 9:
			return { [pick(["anyOf", "oneOf"])]: [gen(ctx, depth - 1), gen(ctx, depth - 1), ...(chance(0.4) ? [gen(ctx, depth - 1)] : [])] };
		case 10:
			return { allOf: [objectSchema(ctx, depth), objectSchema(ctx, depth)] };
		case 11: {
			const props = properties(ctx, depth);
			const names = Object.keys(props);
			return {
				type: "object",
				properties: props,
				[pick(["anyOf", "oneOf"])]: names.map((n) => ({ required: [n] })).concat(chance(0.5) ? [{ required: ["zz"] }] : []),
			};
		}
		case 12:
			return { anyOf: [gen(ctx, depth - 1), { type: "null" }] };
		case 13: {
			const name = `D${Object.keys(ctx.defs).length}`;
			ctx.defs[name] = gen(ctx, depth - 1);
			return { $ref: `#/${ctx.defsKey}/${name}` };
		}
		case 14:
			ctx.recursive = true;
			return { $ref: `#/${ctx.defsKey}/Rec` };
		default:
			if (ctx.reject && chance(0.5)) return { $ref: `#/${ctx.defsKey}/Nope` };
			return { type: "array", items: { type: pick(TYPES) } };
	}
}

function recursiveDefinition(ctx) {
	return pick([
		{ anyOf: [{ type: pick(["string", "number", "integer"]) }, { type: "array", items: { $ref: `#/${ctx.defsKey}/Rec` } }] },
		{
			anyOf: [
				{ type: "string" },
				{ type: "number" },
				{ type: "boolean" },
				{ type: "array", items: { $ref: `#/${ctx.defsKey}/Rec` } },
				{ type: "object", additionalProperties: { $ref: `#/${ctx.defsKey}/Rec` } },
			],
		},
		{
			type: "object",
			properties: { v: { type: "integer" }, next: { $ref: `#/${ctx.defsKey}/Rec` } },
		},
		{ oneOf: [{ type: "number" }, { type: "array", items: { $ref: `#/${ctx.defsKey}/Rec` } }] },
	]);
}

function rootSchema(ctx) {
	let schema = gen(ctx, 3);
	if (typeof schema !== "object" || Object.keys(ctx.defs).length > 0 || ctx.recursive) {
		if (typeof schema !== "object") schema = { allOf: [schema] };
		const defs = { ...ctx.defs };
		if (ctx.recursive) defs.Rec = recursiveDefinition(ctx);
		schema = { ...schema, [ctx.defsKey]: defs };
	}
	return schema;
}

// ------------------------------------------------------------------------------------------------------------------
// Values: a value the schema accepts (as far as the generator follows it), then loosened
// ------------------------------------------------------------------------------------------------------------------

function resolve(ctx, schema, ref) {
	const name = ref.slice(ref.lastIndexOf("/") + 1);
	return schema[ctx.defsKey]?.[name];
}

function valueFor(ctx, root, schema, depth) {
	if (typeof schema !== "object" || schema === null) return primitive();
	if (typeof schema.$ref === "string") {
		const target = resolve(ctx, root, schema.$ref);
		return target === undefined || depth <= 0 ? primitive() : valueFor(ctx, root, target, depth - 1);
	}
	if (Object.hasOwn(schema, "const")) return schema.const;
	if (Array.isArray(schema.enum)) return pick(schema.enum);
	for (const key of ["anyOf", "oneOf"]) {
		if (Array.isArray(schema[key])) {
			const base = valueFor(ctx, root, pick(schema[key]), depth - 1);
			if (schema.properties !== undefined && base !== null && typeof base === "object" && !Array.isArray(base)) return objectValue(ctx, root, schema, depth, base);
			return base;
		}
	}
	if (Array.isArray(schema.allOf)) {
		let merged = {};
		for (const member of schema.allOf) {
			const v = valueFor(ctx, root, member, depth - 1);
			if (v !== null && typeof v === "object" && !Array.isArray(v)) merged = { ...merged, ...v };
		}
		return merged;
	}
	const type = Array.isArray(schema.type) ? pick(schema.type) : schema.type;
	switch (type) {
		case "string":
			return pick(["a", "", "hello", "x y"]);
		case "number":
			return pick(EDGE_NUMBERS);
		case "integer":
			return pick([0, 1, 2, 42, -3]);
		case "boolean":
			return chance(0.5);
		case "null":
			return null;
		case "array": {
			if (depth <= 0) return [];
			const length = int(4);
			const out = [];
			const prefix = Array.isArray(schema.items) ? schema.items : Array.isArray(schema.prefixItems) ? schema.prefixItems : undefined;
			for (let i = 0; i < length; i++) {
				const item = prefix !== undefined && i < prefix.length ? prefix[i] : Array.isArray(schema.items) ? schema.additionalItems : schema.items;
				out.push(item === undefined ? primitive() : valueFor(ctx, root, item, depth - 1));
			}
			return out;
		}
		case "object":
			return objectValue(ctx, root, schema, depth, {});
		default:
			if (schema.properties !== undefined || schema.patternProperties !== undefined) return objectValue(ctx, root, schema, depth, {});
			return primitive();
	}
}

function objectValue(ctx, root, schema, depth, base) {
	const out = { ...base };
	if (depth <= 0) return out;
	const required = Array.isArray(schema.required) ? schema.required : [];
	for (const [key, property] of Object.entries(schema.properties ?? {})) {
		if (!required.includes(key) && chance(0.35)) {
			if (chance(0.5)) out[key] = null;
			continue;
		}
		out[key] = valueFor(ctx, root, property, depth - 1);
	}
	if (schema.patternProperties !== undefined && chance(0.7)) {
		const [, item] = Object.entries(schema.patternProperties)[0];
		out[pick(["n_x", "x1", "12", "a", "b"])] = valueFor(ctx, root, item, depth - 1);
	}
	if (chance(0.3)) {
		out[pick(["other", "extra", "zz"])] = typeof schema.additionalProperties === "object" ? valueFor(ctx, root, schema.additionalProperties, depth - 1) : primitive();
	}
	return out;
}

function primitive() {
	return pick([pick(EDGE_STRINGS), pick(EDGE_NUMBERS), true, false, null, "a", 7]);
}

function loosen(value, depth = 0) {
	if (Array.isArray(value)) {
		if (value.length === 1 && chance(0.25)) return loosen(value[0], depth + 1);
		return value.map((item) => loosen(item, depth + 1));
	}
	if (value !== null && typeof value === "object") {
		if (chance(0.04)) return [loosen({ ...value }, depth + 1)];
		const out = {};
		for (const key of Object.keys(value)) out[key] = loosen(value[key], depth + 1);
		return out;
	}
	if (!chance(0.45)) return value;
	switch (typeof value) {
		case "number":
			return pick([String(value), String(value), value !== 0 ? true : "0", null, "x"]);
		case "string":
			return pick([pick(EDGE_STRINGS), 0, 1, true, null, value + " "]);
		case "boolean":
			return pick([value ? "true" : "false", value ? "TRUE" : "FALSE", value ? 1 : 0, value ? "1" : "0", null, "yes"]);
		default:
			return pick(["null", "", 0, false, "undefined", "NULL", "0", [], {}]);
	}
}

// ------------------------------------------------------------------------------------------------------------------
// Cases
// ------------------------------------------------------------------------------------------------------------------

const out = [];
let thrown = 0;

function record(schema, options, inputs, note) {
	let validator;
	try {
		validator = compile(schema, { formats, coerce: true, ...options });
	} catch (error) {
		return false;
	}
	const values = [];
	for (const input of inputs) {
		const before = enc(input);
		let entry;
		try {
			const coerced = validator.coerce(input);
			const parsed = validator.parse(input);
			entry = {
				input: before,
				coerced: enc(coerced),
				same: Object.is(coerced, input),
				parse: parsed.ok ? { ok: true, value: enc(parsed.value) } : { ok: false },
			};
		} catch (error) {
			thrown++;
			continue;
		}
		if (enc(input) !== before) throw new Error(`Pi modified its input ${before}`);
		values.push(entry);
	}
	const parts = [`"schema":${enc(schema)}`, `"options":${JSON.stringify(options)}`];
	if (note !== undefined) parts.push(`"note":${JSON.stringify(note)}`);
	parts.push(`"values":[${values.map((v) => `{"input":${v.input},"coerced":${v.coerced},"same":${v.same},"parse":${v.parse.ok ? `{"ok":true,"value":${v.parse.value}}` : `{"ok":false}`}}`).join(",")}]`);
	out.push(`{${parts.join(",")}}`);
	return true;
}

// Generated schemas.
const COUNT = 420;
for (let i = 0; i < COUNT; i++) {
	const draft = chance(0.2) ? "7" : "2020-12";
	const reject = chance(0.15);
	const ctx = { draft, reject, defsKey: draft === "7" ? "definitions" : "$defs", defs: {}, recursive: false };
	const schema = rootSchema(ctx);
	const inputs = [];
	for (let k = 0; k < 6; k++) {
		const base = valueFor(ctx, schema, schema, 4);
		inputs.push(k === 0 ? base : loosen(base));
	}
	const options = { ...(draft === "7" ? { draft } : {}), ...(reject ? { unresolvedRefs: "reject" } : {}) };
	record(schema, options, inputs);
}

// Tool-like schemas with loose arguments.
const readTool = { type: "object", properties: { path: { type: "string" }, offset: { type: "integer", minimum: 1 }, limit: { type: "integer", minimum: 1 } }, required: ["path"], additionalProperties: false };
const editTool = {
	type: "object",
	properties: { path: { type: "string" }, edits: { type: "array", items: { type: "object", properties: { oldText: { type: "string" }, newText: { type: "string" } }, required: ["oldText", "newText"], additionalProperties: false } } },
	required: ["path", "edits"],
	additionalProperties: false,
};
const bashTool = { type: "object", properties: { command: { type: "string" }, timeout: { type: ["number", "null"] }, env: { type: "object", additionalProperties: { type: "string" } }, verbose: { type: "boolean" } }, required: ["command"] };
record(readTool, {}, [
	{ path: "a.txt", offset: "10", limit: "5" },
	{ path: "a.txt", offset: null, limit: 3.5 },
	{ path: 5, offset: "ten" },
	{ path: "a.txt" },
	{ path: null },
	{ path: "a", offset: "0x10", limit: " 7 " },
	{ path: "a", offset: "1e2", limit: "1_0" },
]);
record(editTool, {}, [
	{ path: "a", edits: [{ oldText: "x", newText: "y" }] },
	{ path: "a", edits: { oldText: 1, newText: true } },
	{ path: "a", edits: [{ oldText: "x", newText: null }, { oldText: 2, newText: 3 }] },
	{ path: "a", edits: "x" },
	{ path: "a", edits: [] },
	{ path: "a", edits: [{ oldText: "x", newText: "y", extra: 1 }] },
]);
record(bashTool, {}, [
	{ command: "ls", timeout: "30", env: { A: 1, B: true, C: null }, verbose: "TRUE" },
	{ command: 5, timeout: null, verbose: 1 },
	{ command: "ls", timeout: "", verbose: "" },
	{ command: ["a"], timeout: "abc" },
]);

// Recursive schemas, as in coerce.test.ts, with their values.
const tree = { type: "object", properties: { t: { $ref: "#/$defs/Tree" } }, $defs: { Tree: { anyOf: [{ type: "string" }, { type: "array", items: { $ref: "#/$defs/Tree" } }] } } };
record(tree, {}, [{ t: ["a", ["b"]] }, { t: 5 }, { t: [5] }, { t: {} }, { t: { a: 1 } }, { t: [[[1, null]]] }, { t: [true, [false, ["x"]]] }]);
const jsonSchema = {
	type: "object",
	properties: { data: { $ref: "#/$defs/Json" } },
	required: ["data"],
	$defs: { Json: { anyOf: [{ type: "string" }, { type: "number" }, { type: "boolean" }, { type: "array", items: { $ref: "#/$defs/Json" } }, { type: "object", additionalProperties: { $ref: "#/$defs/Json" } }] } },
};
record(jsonSchema, {}, [{ data: { a: [1, null] } }, { data: "x" }, { data: null }, { data: [null, [null]] }]);
const selfRef = { type: "object", properties: { value: { anyOf: [{ type: "number" }, { type: "array", items: { $ref: "#/properties/value" } }] } }, required: ["value"] };
record(selfRef, {}, [{ value: "42" }, { value: ["1", ["2"]] }, { value: {} }, { value: [{}] }]);
const rootRef = { anyOf: [{ type: "number" }, { type: "array", items: { $ref: "#" } }] };
record(rootRef, {}, [{}, "42", [1, "2", ["3"]], [{}], [[[]]]]);
let deep = { x: 1 };
for (let i = 0; i < 30; i++) deep = { c: deep };
const S = { anyOf: [{ type: "object", additionalProperties: { $ref: "#/$defs/S" }, required: ["a"] }, { type: "object", additionalProperties: { $ref: "#/$defs/S" }, required: ["b"] }] };
record({ type: "object", properties: { s: { $ref: "#/$defs/S" } }, $defs: { S } }, {}, [{ s: deep }, { s: { c: { a: 1 } } }]);
const cyclic = { type: "object", required: ["t"], properties: { t: { $defs: { Tree: { anyOf: [{ type: "string" }, { type: "array", items: { $ref: "Tree" } }], $id: "Tree" } }, $ref: "Tree" } } };
record(cyclic, {}, [{ t: [5] }, { t: { a: 1 } }, { t: "x" }, { t: [[1], 2] }]);
record({ type: "object", properties: { t: { $ref: "#/definitions/Missing" }, u: { type: "integer" } } }, { unresolvedRefs: "reject" }, [{ t: "1", u: "2" }, { u: "x" }, {}]);

// Every conversion helper on every edge string and number, for each type.
for (const type of ["string", "number", "integer", "boolean", "null"]) {
	const inputs = [...EDGE_STRINGS, ...EDGE_NUMBERS, true, false, null, [], {}, [1], { a: 1 }].map((value) => ({ value }));
	record({ type: "object", properties: { value: { type } }, required: ["value"] }, {}, inputs, `edge values for type ${type}`);
	record({ type: "object", properties: { value: { type: [type, "null"] } }, required: ["value"] }, {}, inputs, `edge values for type [${type}, null]`);
	record({ type: "object", properties: { value: { type: "array", items: { type } } }, required: ["value"] }, {}, inputs, `edge values for items of type ${type}`);
	record({ type: "object", properties: { value: { type: "object", additionalProperties: { type } } }, required: ["value"] }, {}, inputs.slice(0, 20).map(({ value }) => ({ value: { k: value } })), `edge values for additionalProperties of type ${type}`);
}
for (const literal of ["1", "true", "null", "a", 0, 1, 2.5, true, false]) {
	const inputs = [...EDGE_STRINGS, ...EDGE_NUMBERS, true, false, null].map((value) => ({ value }));
	record({ type: "object", properties: { value: { const: literal } }, required: ["value"] }, {}, inputs, `const ${JSON.stringify(literal)}`);
	record({ type: "object", properties: { value: { enum: [literal, "z", 99] } }, required: ["value"] }, {}, inputs, `enum ${JSON.stringify(literal)}`);
}

if (thrown > 0) console.error(`Pi threw for ${thrown} values`);
writeFileSync(process.argv[3], `{"piCommit":${JSON.stringify(commit)},"thrown":${thrown},"cases":[\n${out.join(",\n")}\n]}\n`);
console.error(`${out.length} schemas`);
