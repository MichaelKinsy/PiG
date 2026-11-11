// Generates schema/testdata/errors-golden.json: for every group and test of the vendored JSON-Schema-Test-Suite, Pi's
// check result and errors (packages/schema at Pi commit b556cfe5), plus the speed cases of test/bench/cases.ts.
// Run through Node 24 against a checkout of Pi: node gen-errors.mjs <pi>/packages/schema <out.json>
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
const root = process.argv[2];
const { compile } = await import(`${root}/src/index.ts`);
const { formats } = await import(`${root}/src/formats/index.ts`);
const { suiteFiles, suiteSchemas } = await import(`${root}/test/suite.ts`);
const { cases } = await import(`${root}/test/bench/cases.ts`);
const { corpus } = await import(`${root}/test/bench/corpus.ts`);
const schemas = suiteSchemas();
const out = { suite: [], cases: [], extras: [], corpus: {} };
for (const [count, seed] of [[200, 42], [100, 7], [1000, 99]]) {
	out.corpus[`${count}:${seed}`] = createHash("sha256").update(JSON.stringify(corpus(count, seed))).digest("hex");
}
for (const file of suiteFiles()) {
	const options = { draft: file.draft, schemas, formats: file.name.includes("/optional/format/") ? formats : undefined };
	file.groups.forEach((group, gi) => {
		let validator;
		let error;
		try {
			validator = compile(group.schema, options);
		} catch (e) {
			error = String(e.message);
		}
		const row = { file: file.name, group: gi };
		if (error !== undefined) row.compileError = error;
		else row.tests = group.tests.map((t) => ({ check: validator.check(t.data), errors: validator.errors(t.data) }));
		out.suite.push(row);
	});
}
for (const c of cases) {
	const validator = compile(c.schema, { draft: "2020-12" });
	out.cases.push({
		name: c.name,
		schema: c.schema,
		validValue: c.valid,
		invalidValue: c.invalid,
		valid: { check: validator.check(c.valid), errors: validator.errors(c.valid) },
		invalid: { check: validator.check(c.invalid), errors: validator.errors(c.invalid) },
	});
}

// Hand-written cases for what the suite leaves out: messages, error order and paths.
const block = (kind, extra) => ({ type: "object", properties: { kind: { const: kind }, ...extra }, required: ["kind", ...Object.keys(extra)], additionalProperties: false });
const extras = [
	{ schema: { type: "string", minLength: 3, maxLength: 4 }, values: ["\u{1d4b3}\u{1d4b4}", "ab", "abc", "\u{1d4b3}\u{1d4b4}\u{1d4b3}\u{1d4b4}\u{1d4b3}", 5] },
	{ schema: { type: "number", minimum: 1e21, maximum: 1e-7, multipleOf: 0.0001 }, values: [0.0075, 1e21, 5, 1e22, "x"] },
	{ schema: { type: "number", exclusiveMinimum: 0.5, exclusiveMaximum: 100.25, multipleOf: 0.1 }, values: [0.5, 100.25, 0.3, 0.35, 1e-7, 42] },
	{ schema: { type: ["string", "null"] }, values: [1, 1.5, [], {}, true, null, "a"] },
	{ schema: { type: ["integer", "boolean", "array"] }, values: ["a", 1.5, {}, null] },
	{ schema: { enum: [{ b: 1, a: 2 }, [1, 2], "x", null, 1.5, 1e21] }, values: [{ a: 2, b: 1 }, [1, 2], [2, 1], "y", 1e21, 1.5, 2] },
	{ schema: { const: { z: [1, { y: null }], a: "\u2028<>&\"\\" } }, values: [{ z: [1, { y: null }], a: "\u2028<>&\"\\" }, { z: [1, { y: 0 }], a: "" }, null] },
	{ schema: { oneOf: [{ type: "integer" }, { minimum: 0 }] }, values: [5, -1, 1.5, "x", -1.5] },
	{ schema: { anyOf: [{ type: "object", properties: { a: { type: "object", properties: { b: { type: "integer" } } } } }, { type: "object", properties: { a: { type: "string" } } }, { type: "array" }] }, values: [{ a: { b: "x" } }, { a: 1 }, 7] },
	{ schema: { anyOf: [{ type: "string" }, { type: "number" }] }, values: [null, true, {}] },
	{ schema: { not: { type: "string" } }, values: ["a", 1] },
	{ schema: { if: { properties: { a: { const: 1 } }, required: ["a"] }, then: { required: ["b"] }, else: { required: ["c"] } }, values: [{ a: 1 }, { a: 2 }, { a: 1, b: 1 }, {}] },
	{ schema: { type: "array", contains: { type: "integer" }, minContains: 2, maxContains: 3 }, values: [[], [1], [1, 2], [1, 2, 3, 4], ["a", 1, 2]] },
	{ schema: { type: "array", contains: { type: "string" } }, values: [[], [1], ["a"]] },
	{ schema: { type: "object", patternProperties: { "^x-": { type: "integer" }, "[0-9]$": { type: "string" } }, additionalProperties: { type: "boolean" }, propertyNames: { maxLength: 4 } }, values: [{ "x-a": "s", b1: 1, "x-1": 2, other: 1, longname: true, "": 1 }] },
	{ schema: { type: "object", minProperties: 2, maxProperties: 3, dependentRequired: { a: ["b", "c"] }, dependentSchemas: { d: { required: ["e"] } } }, values: [{}, { a: 1 }, { d: 1, a: 1 }, { a: 1, b: 1, c: 1, d: 1, e: 1 }] },
	{ draft: "7", schema: { type: "object", dependencies: { a: ["b"], c: { required: ["d"] } } }, values: [{ a: 1 }, { c: 1 }, { a: 1, b: 1, c: 1, d: 1 }] },
	{ schema: { type: "object", allOf: [{ properties: { a: true } }], properties: { b: true }, unevaluatedProperties: false }, values: [{ a: 1, b: 2, c: 3, d: 4 }, { a: 1 }] },
	{ schema: { type: "object", allOf: [{ properties: { a: true } }], unevaluatedProperties: { type: "integer" } }, values: [{ a: "x", b: "y", c: 1 }] },
	{ schema: { type: "array", prefixItems: [{ type: "integer" }], unevaluatedItems: false }, values: [[1], [1, 2, 3], ["a", 2]] },
	{ schema: { type: "array", prefixItems: [{ type: "integer" }], contains: { const: "x" }, unevaluatedItems: { type: "integer" } }, values: [[1, "x", 2], [1, "x", "y"], [1, "z"]] },
	{ schema: { type: "object", additionalProperties: false }, values: [{ b: 1, 2: 1, a: 1, 1: 1, "-1": 1 }] },
	{ draft: "7", schema: { type: "array", items: [{ type: "integer" }, { type: "string" }], additionalItems: { type: "boolean" } }, values: [[1, "a", true], ["a", 1, 2], [1]] },
	{ schema: { $defs: { s: { type: "string" } }, $ref: "#/$defs/s", maxLength: 1 }, values: ["ab", 1] },
	{ draft: "7", schema: { definitions: { s: { type: "string" } }, $ref: "#/definitions/s", maxLength: 1 }, values: ["ab", 1] },
	{ schema: { type: "object", properties: { "\u00e9/~": { type: "integer" }, "": { type: "integer" } } }, values: [{ "\u00e9/~": "x", "": "y" }] },
	{ schema: { properties: { a: false, b: true } }, values: [{ a: 1, b: 1 }, { b: 1 }] },
	{ schema: false, values: [1, null] },
	{ schema: { type: "string", format: "email", pattern: "^a", minLength: 5 }, formats: true, values: ["b", "a@example.com", "ab@", 1] },
	{ schema: { type: "array", uniqueItems: true, items: { type: "object" } }, values: [[{ a: 1, b: 2 }, { b: 2, a: 1 }], [{ a: 1 }, { a: 2 }], [1, 1.0], ["1", 1]] },
	{ schema: { type: "array", items: block("text", { text: { type: "string" } }) }, values: [[{ kind: "text", text: 1 }, { kind: "text" }]] },
	{ schema: { $defs: { text: block("text", { text: { type: "string" } }), image: block("image", { width: { type: "integer" } }) }, anyOf: [{ $ref: "#/$defs/text" }, { $ref: "#/$defs/image" }] }, values: [{ kind: "image", width: "x" }, { kind: "video" }, {}, 5, { kind: null }] },
	{ schema: { oneOf: [{ type: "object", properties: { t: { const: 1 }, a: { type: "string" } }, required: ["t"] }, { type: "object", properties: { t: { const: null }, a: { type: "integer" } }, required: ["t"] }] }, values: [{ t: 1, a: 2 }, { t: null, a: "x" }, { t: 2 }, { a: 1 }] },
	{ schema: { anyOf: [{ const: "a" }, { const: "b" }, { const: 3 }, { const: null }] }, values: ["c", 3, null, 4, "a"] },
	{ schema: { type: "object", properties: { a: { type: "object", properties: { b: { type: "array", items: { type: "object", required: ["c"], properties: { c: { type: "integer", minimum: 5 } } } } } } } }, values: [{ a: { b: [{ c: 1 }, {}, { c: 9 }, { c: "x" }] } }] },
	{ schema: { $id: "https://example.com/root", $ref: "list", $defs: { foo: { $dynamicAnchor: "items", type: "string" }, list: { $id: "list", type: "array", items: { $dynamicRef: "#items" }, $defs: { items: { $dynamicAnchor: "items" } } } } }, values: [["a", "b"], ["a", 1], 5] },
	{ schema: { type: "object", required: ["a", "b"], properties: { a: { type: "string" } }, additionalProperties: { type: "integer" } }, values: [{ a: 1, c: "x" }, { b: 1, d: 2.5 }] },
	{ schema: { type: "object", required: ["a"], additionalProperties: false }, values: [{ b: 1 }, { a: 1 }] },
	{ schema: { type: "integer", multipleOf: 0.0001, minimum: -5 }, values: [3, -6, 1e308, 1e-9] },
	{ schema: { type: "object", properties: { a: { type: "integer" } }, patternProperties: { "^x-": { type: "string" } }, unevaluatedProperties: false }, values: [{ a: "s", "x-b": 1, c: true }, { a: 1, "x-b": "s", c: true }] },
	{ schema: { $id: "file:///a/b.json", $ref: "c.json" }, values: [1] },
	{ schema: { $id: "custom://host/p/q.json", $ref: "../r.json#/x" }, values: [1] },
	{ schema: { $id: "custom://host/p/q.json", properties: { a: { $id: "s.json", $ref: "t.json" } } }, values: [{}] },
	{ schema: { $id: "urn:example:root", $ref: "c.json" }, values: [1] },
	{ schema: { $id: "urn:example:root", $defs: { a: { $anchor: "x", type: "string" } }, $ref: "#x" }, values: [1, "a"] },
	{ schema: { $id: "http://example.com/a/b/c.json", $ref: "../../d.json" }, values: [1] },
	{ schema: { $id: "HTTP://Example.COM:80/a/./b/../c.json", $ref: "d.json" }, values: [1] },
	{ schema: { if: 5, minimum: 3 }, values: [1, 5] },
	{ schema: { $id: "https://ex/root", $ref: "list", $defs: { bad: { $dynamicAnchor: "items", $ref: "#/nope" }, list: { $id: "list", type: "array", items: { $dynamicRef: "#items" }, $defs: { items: { $dynamicAnchor: "items" } } } } }, values: [["a"], 5, []] },
	{ schema: { $id: "file:///C:/a/b.json", $ref: "../../x.json" }, values: [1] },
	{ schema: { $id: "file:///C:/a/b.json", $ref: "/x.json" }, values: [1] },
	{ schema: { $id: "file:///a/b.json", $ref: "c\\d.json" }, values: [1] },
	{ schema: { $id: "file://localhost/a/b.json", $ref: "x.json" }, values: [1] },
	{ schema: { $id: "custom:/a/b", $ref: "?q" }, values: [1] },
	{ schema: { $id: "urn:a:b", $ref: "#/$defs/x", $defs: { x: { type: "string" } } }, values: [1, "a"] },
	{ schema: { type: "string", minLength: 1, maxLength: 1 }, values: ["\ud800", "\ud800\ud800", "\ud83d\udc32", "\udc32\ud83d", "a"] },
	{ schema: { type: "string", minLength: 2 }, values: ["\ud800", "\ud800\ud800", "\ud83d\udc32"] },
	{ schema: { pattern: "^.$" }, values: ["\ud800", "\ud83d\udc32", "\ud800a", "\udc32\ud83d"] },
	{ schema: { enum: ["\ud800", "a"] }, values: ["\ud801", "a", "\ud800"] },
	{ schema: { const: "\ud800" }, values: ["\ud800", "\ufffd"] },
	{ schema: { type: "object", properties: { "\ud800": { type: "integer" } }, additionalProperties: false }, values: [{ "\ud800": "x" }, { "\udc00": 1 }] },
	{ schema: { minLength: 2, minimum: 3, minItems: 1, minProperties: 1, required: ["x"] }, values: ["a", 1, [], {}, null] },
];
const formatsOnly = { ...formats };
for (const x of extras) {
	const options = { draft: x.draft ?? "2020-12", formats: x.formats ? formats : undefined };
	const row = { schema: x.schema, draft: options.draft, formats: x.formats === true, values: x.values };
	try {
		const validator = compile(x.schema, options);
		row.results = x.values.map((v) => {
			const result = {};
			try {
				result.check = validator.check(v);
			} catch (e) {
				result.checkThrows = String(e.message);
				return result;
			}
			try {
				result.errors = validator.errors(v);
			} catch (e) {
				result.errorsThrows = String(e.message);
			}
			return result;
		});
	} catch (e) {
		row.compileError = String(e.message);
	}
	out.extras.push(row);
}
void formatsOnly;
writeFileSync(process.argv[3], JSON.stringify(out));
