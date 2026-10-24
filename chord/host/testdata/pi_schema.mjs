// Regenerates pi_schema.json: Pi's packages/chord/src/schema.ts (canonicalJson, hash64, fits, canonicalIdentity, normalize)
// run over the inputs below. Run from the repository root with Node 24 or later:
//
//	node chord/host/testdata/pi_schema.mjs > chord/host/testdata/pi_schema.json
//
// Each case holds its inputs (JSON text, so -0, 1e21 and key order survive) and what Pi's code returned for them; the Go
// test replays the inputs through chord/host. The fits pairs are curated edge cases plus seeded random schema pairs.
import { Type } from "../../../.upstream/current/node_modules/typebox/build/index.mjs";
import { canonicalIdentity, canonicalJson, fits, hash64, normalize } from "../../../.upstream/current/packages/chord/src/schema.ts";

function mulberry32(seed) {
	let state = seed >>> 0;
	return () => {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const canonicalTexts = [
	"null",
	"true",
	"false",
	"0",
	"-0",
	"1.5",
	"1e21",
	"1e-7",
	"123456789012345680000",
	"0.1",
	"\"\"",
	"\"a\\u2028<&>\\n\\t\\u0001\"",
	"[]",
	"{}",
	"[1,[2,[3,{\"b\":1,\"a\":2}]]]",
	"{\"b\":1,\"a\":{\"z\":1,\"y\":[{\"d\":1,\"c\":2}]},\"10\":1,\"9\":2,\"\":3}",
	"{\"\\ud83d\\ude00\":1,\"\\uff61\":2,\"\\uffff\":3,\"\\ue000\":4,\"b\":5,\"B\":6,\"ä\":7}",
	"{\"a\":null,\"b\":[null,{\"c\":null}]}",
	"{\"__proto__\":1,\"constructor\":2}",
	"[1e21,-0,0.1,\"\\u2028<&>\"]",
];
const canonical = canonicalTexts.map((text) => ({ text, expect: canonicalJson(JSON.parse(text)) }));

const hashTexts = ["", "a", "ab", "abc", "chord", "a😀", "😀", "\uffff", "ä", "x".repeat(1000), "{\"a\":1}", "\n\t\u2028"];
const hashes = hashTexts.map((text) => ({ text, expect: hash64(text) }));

const curatedFits = [
	[{ type: "integer" }, { type: "number" }],
	[{ type: "number" }, { type: "integer" }],
	[{ type: "string" }, {}],
	[{}, { type: "string" }],
	[{ type: "string" }, false],
	[false, { type: "string" }],
	[{ type: "string", description: "a" }, { type: "string", title: "b", default: "x" }],
	[{ type: "string", minLength: 1 }, { type: "string", minLength: 1 }],
	[{ type: "string", minLength: 2 }, { type: "string", minLength: 1 }],
	[{ type: "string" }, { type: "string", minLength: 1 }],
	[{ const: "a" }, { const: "a" }],
	[{ const: "a" }, { const: "b" }],
	[{ type: "string" }, { const: "a" }],
	[{ const: "a" }, { enum: ["a", "b"] }],
	[{ enum: ["a"] }, { enum: ["a", "b"] }],
	[{ enum: ["a", "c"] }, { enum: ["a", "b"] }],
	[{ type: "string" }, { enum: ["a", "b"] }],
	[{ anyOf: [{ type: "string" }, { type: "integer" }] }, { anyOf: [{ type: "string" }, { type: "number" }] }],
	[{ anyOf: [{ type: "string" }, { type: "boolean" }] }, { anyOf: [{ type: "string" }, { type: "number" }] }],
	[{ type: "string" }, { anyOf: [{ type: "number" }, { type: "string" }] }],
	[{ type: "string" }, { oneOf: [{ type: "string" }] }],
	[{ type: "string" }, { type: "string", pattern: "^a" }],
	[{ type: ["string", "null"] }, { type: ["string", "null", "number"] }],
	[{ type: ["string", "number"] }, { type: "string" }],
	[{ type: "object", properties: { a: { type: "string" } }, required: ["a"] }, { type: "object", properties: { a: { type: "string" } }, required: ["a"] }],
	[{ type: "object", properties: { a: { type: "string" } } }, { type: "object", properties: { a: { type: "string" } }, required: ["a"] }],
	[{ type: "object", properties: { a: { type: "integer" }, b: { type: "string" } }, required: ["a"] }, { type: "object", properties: { a: { type: "number" } }, required: ["a"] }],
	[{ type: "object", properties: { a: { type: "integer" }, b: { type: "string" } }, required: ["a"] }, { type: "object", properties: { a: { type: "number" } }, required: ["a"], additionalProperties: false }],
	[{ type: "object", properties: { a: { type: "integer" } }, required: ["a"], additionalProperties: false }, { type: "object", properties: { a: { type: "number" }, b: { type: "string" } }, required: ["a"], additionalProperties: false }],
	[{ type: "object", properties: { a: { type: "string" } }, additionalProperties: { type: "number" } }, { type: "object", properties: { b: { type: "number" } } }],
	[{ type: "object", properties: { a: { type: "string" } }, additionalProperties: { type: "string" } }, { type: "object", properties: { b: { type: "number" } } }],
	[{ type: "object", additionalProperties: { type: "string" } }, { type: "object", additionalProperties: { type: "string" } }],
	[{ type: "object", additionalProperties: { type: "string" } }, { type: "object", additionalProperties: { type: "number" } }],
	[{ type: "object", properties: { aa: { type: "integer" } }, additionalProperties: false }, { type: "object", patternProperties: { "^a": { type: "number" } } }],
	[{ type: "object", properties: { aa: { type: "string" } }, additionalProperties: false }, { type: "object", patternProperties: { "^a": { type: "number" } } }],
	[{ type: "object", patternProperties: { "^a": { type: "integer" } }, additionalProperties: false }, { type: "object", patternProperties: { "^a": { type: "number" } } }],
	[{ type: "object", patternProperties: { "^b": { type: "integer" } }, additionalProperties: false }, { type: "object", patternProperties: { "^a": { type: "number" } } }],
	[{ type: "object", properties: { a: { type: "string" } } }, { type: "object", patternProperties: { "^a": { type: "string" } } }],
	[{ type: "array", items: { type: "integer" } }, { type: "array", items: { type: "number" } }],
	[{ type: "array", items: { type: "number" } }, { type: "array", items: { type: "integer" } }],
	[{ type: "array", items: [{ type: "integer" }, { type: "string" }] }, { type: "array", items: [{ type: "number" }, { type: "string" }] }],
	[{ type: "array", items: [{ type: "integer" }] }, { type: "array", items: [{ type: "number" }, { type: "string" }] }],
	[{ type: "array", items: [{ type: "integer" }, { type: "integer" }] }, { type: "array", items: { type: "number" } }],
	[{ type: "array", items: { type: "integer" }, minItems: 1 }, { type: "array", items: { type: "number" }, minItems: 1 }],
	[{ type: "array", items: { type: "integer" } }, { type: "array", items: { type: "number" }, minItems: 1 }],
	[{ type: "function", parameters: [{ type: "number" }], returnType: { type: "integer" } }, { type: "function", parameters: [{ type: "integer" }], returnType: { type: "number" } }],
	[{ type: "function", parameters: [{ type: "integer" }], returnType: { type: "integer" } }, { type: "function", parameters: [{ type: "number" }], returnType: { type: "number" } }],
	[{ type: "function", parameters: [{ type: "number" }, { type: "number" }], returnType: { type: "integer" } }, { type: "function", parameters: [{ type: "integer" }], returnType: { type: "number" } }],
	[{ type: "function", parameters: [], returnType: { type: "integer" } }, { type: "function", parameters: [{ type: "integer" }], returnType: { type: "number" } }],
	[{ type: "object", properties: { a: { type: "string" } }, required: ["a"] }, { type: "object", properties: { a: { type: "string" } }, required: ["a"], additionalProperties: { type: "number" } }],
	[{ type: "object" }, { type: "object", additionalProperties: true }],
];

const TYPES = ["string", "number", "integer", "boolean", "null", "object", "array", "function"];
const PICK = (random, list) => list[Math.floor(random() * list.length)];
function randomSchema(random, depth) {
	const roll = random();
	if (depth <= 0 || roll < 0.3) {
		const schema = { type: PICK(random, TYPES.slice(0, 5)) };
		if (random() < 0.15) schema.minLength = PICK(random, [1, 2]);
		if (random() < 0.1) schema.minimum = PICK(random, [0, 1]);
		if (random() < 0.1) schema.description = "d";
		return schema;
	}
	if (roll < 0.36) return { const: PICK(random, ["a", "b", 1, null]) };
	if (roll < 0.42) return { enum: [PICK(random, ["a", "b"]), PICK(random, ["b", "c"])] };
	if (roll < 0.52) return { anyOf: [randomSchema(random, depth - 1), randomSchema(random, depth - 1)] };
	if (roll < 0.7) {
		const properties = {};
		for (const key of ["a", "b", "ab"]) if (random() < 0.5) properties[key] = randomSchema(random, depth - 1);
		const schema = { type: "object", properties };
		const required = Object.keys(properties).filter(() => random() < 0.5);
		if (required.length > 0) schema.required = required;
		const extra = random();
		if (extra < 0.25) schema.additionalProperties = false;
		else if (extra < 0.35) schema.additionalProperties = randomSchema(random, depth - 1);
		else if (extra < 0.4) schema.additionalProperties = true;
		if (random() < 0.2) schema.patternProperties = { "^a": randomSchema(random, depth - 1) };
		return schema;
	}
	if (roll < 0.85) {
		const schema = { type: "array" };
		if (random() < 0.3) schema.items = [randomSchema(random, depth - 1), randomSchema(random, depth - 1)].slice(0, 1 + Math.floor(random() * 2));
		else schema.items = randomSchema(random, depth - 1);
		if (random() < 0.15) schema.minItems = 1;
		return schema;
	}
	return {
		type: "function",
		parameters: Array.from({ length: Math.floor(random() * 3) }, () => randomSchema(random, depth - 1)),
		returnType: randomSchema(random, depth - 1),
	};
}
const random = mulberry32(20261023);
const randomFits = [];
for (let index = 0; index < 600; index++) {
	const sub = randomSchema(random, 2);
	// Half the pairs relate: sup is a perturbed copy of sub, so both outcomes are common.
	const sup = random() < 0.5 ? randomSchema(random, 2) : JSON.parse(JSON.stringify(sub));
	if (sup && typeof sup === "object" && random() < 0.5 && sup.type === "integer") sup.type = "number";
	if (sup && typeof sup === "object" && random() < 0.2 && sup.properties) delete sup.properties.a;
	randomFits.push([sub, sup]);
}
const fitsCases = [...curatedFits, ...randomFits].map(([sub, sup]) => ({ sub, sup, expect: fits(sub, sup) }));

const identitySchema = {
	type: "object",
	properties: {
		limit: { type: "integer", default: 10 },
		tags: { type: "array", items: { type: "string", default: "x" }, default: [] },
		nested: { type: "object", properties: { on: { type: "boolean", default: false }, name: { type: "string" } }, default: { on: false } },
		tuple: { type: "array", items: [{ type: "integer", default: 1 }, { type: "string", default: "z" }] },
	},
};
const identityValues = [
	{},
	{ limit: 10 },
	{ limit: 11, tags: [] },
	{ tags: ["x", "y"], nested: { on: false, name: "n" } },
	{ nested: { on: false } },
	{ nested: { on: true } },
	{ tuple: [1, "z"] },
	{ tuple: [2, "z"], extra: { k: 1 } },
	{ limit: 10, unknown: [1, 2] },
];
const identities = identityValues.map((value) => ({ schema: identitySchema, value, expect: canonicalIdentity(identitySchema, value) }));

const fields = {
	limit: Type.Integer({ default: 10 }),
	mode: Type.String({ default: "fast" }),
	name: Type.String(),
	nested: Type.Optional(Type.Object({ on: Type.Boolean({ default: false }) })),
};
const normalizeSchemas = [Type.Object(fields), Type.Object(fields, { additionalProperties: false }), Type.Object(fields, { additionalProperties: Type.Number() })];
const normalizeInputs = [
	{ name: "a" },
	{ name: "a", limit: 3, extra: 1 },
	{ name: "a", extra: { deep: [1, 2] }, other: "s" },
	{ name: "a", nested: {} },
	{ name: "a", nested: { on: true, junk: 1 } },
	{},
	null,
	{ name: 1 },
	{ name: "a", limit: "x" },
	{ name: "a", limit: 1.5, mode: 3 },
	{ limit: "x", mode: 3, nested: { on: "no" } },
];
const normalizes = normalizeSchemas.flatMap((schema) =>
	normalizeInputs.map((value) => {
		const plain = JSON.parse(JSON.stringify(schema));
		try {
			return { schema: plain, what: "input of thing", value, ok: true, expect: normalize(schema, value, "input of thing") };
		} catch (error) {
			return { schema: plain, what: "input of thing", value, ok: false, expect: { name: error.name, message: error.message, code: error.code, reason: error.reason, details: error.details } };
		}
	}),
);

process.stdout.write(`${JSON.stringify({ canonical, hashes, fits: fitsCases, identities, normalizes }, null, "\t")}\n`);
