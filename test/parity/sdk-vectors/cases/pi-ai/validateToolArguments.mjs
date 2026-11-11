// pi-ai's validateToolArguments (Pi b556cfe5): pi-schema compiles the tool's JSON Schema with coercion, the arguments are coerced (parse) and checked, and a failure is the errors of pi-schema, then the arguments received.
// An argument is [tool, toolCall]; tool.parameters is plain JSON Schema (a TypeBox or pi-schema builder value is that JSON). Type.Refine refinements, codecs and the other TypeBox constructs outside JSON Schema do not apply.
// The answer is the validated arguments, or the error message. ai/validate_vectors_test.go reads these vectors.
const tool = (parameters, name = "echo") => ({ name, description: "d", parameters });
const call = (args, name = "echo") => ({ type: "toolCall", id: "c1", name, arguments: args });
const value = (type, extra = {}) => ({ type: "object", properties: { value: type ? { type, ...extra } : extra }, required: ["value"] });

const cases = [];
const add = (name, parameters, args, toolName = "echo") => cases.push({ name, args: [tool(parameters, toolName), call(args, toolName)] });

// packages/ai/test/validation.test.ts: the coercions of plain JSON schemas, like TypeBox schemas
for (const [type, input] of [["number", "42"], ["number", true], ["number", null], ["integer", "42"], ["integer", "42.1"], ["boolean", "true"], ["boolean", "false"], ["boolean", 1], ["boolean", 0], ["boolean", "1"], ["boolean", "0"],
  ["string", null], ["string", true], ["null", ""], ["null", 0], ["null", false], ["null", "null"]]) {
  add(`${type} from ${JSON.stringify(input)}`, value(type), { value: input });
}
add("an array of numbers from one value", value("array", { items: { type: "number" } }), { value: "7" });
add("a number or string keeps the string", value(undefined, { type: ["number", "string"] }), { value: "1" });
add("a boolean or number takes the number", value(undefined, { type: ["boolean", "number"] }), { value: "1" });
add("a nullable union arm keeps null", value(undefined, { anyOf: [{ type: "number" }, { type: "null" }] }), { value: null });
add("a nullable union arm converts the number", value(undefined, { anyOf: [{ type: "number" }, { type: "null" }] }), { value: "42" });
add("a nullable oneOf arm keeps null", value(undefined, { oneOf: [{ type: "number" }, { type: "null" }] }), { value: null });
add("a nullable array keeps null", value(undefined, { type: ["array", "null"], items: { type: "string" } }), { value: null });
add("a referenced nullable optional keeps null", { type: "object", properties: { value: { $ref: "#/$defs/value" } }, $defs: { value: { anyOf: [{ type: "number" }, { type: "null" }] } } }, { value: null });
add("an optional null is omitted", { type: "object", properties: { path: { type: "string" }, offset: { type: "number" }, nullable: { anyOf: [{ type: "string" }, { type: "null" }] }, metadata: { type: "object", properties: { enabled: { type: "boolean" } } } }, required: ["path", "metadata"] },
  { path: "file.txt", offset: null, nullable: null, metadata: { enabled: null } });
for (const [type, input] of [["boolean", "yes"], ["number", "abc"], ["null", "nil"], ["integer", "4x"]]) add(`${type} rejects ${JSON.stringify(input)}`, value(type), { value: input });
add("errors, then the arguments received", value("number"), { value: "many" });
add("a missing required property is reported at the property", value("string"), {});
add("two errors in the schema's member order", { type: "object", properties: { zeta: { type: "string" }, alpha: { type: "string" } } }, { alpha: [1], zeta: [2] });
add("the received arguments keep the call's member order", { type: "object", properties: { path: { type: "string" } }, required: ["path"] }, { zeta: 1, alpha: { y: 2, b: 3 } });

// the shapes of the built-in tools
const read = { type: "object", required: ["path"], properties: { path: { type: "string" }, offset: { type: "number" }, limit: { type: "number" } } };
add("read: valid", read, { path: "a.txt", offset: 2, limit: 10 }, "read");
add("read: numeric strings and a null optional", read, { path: "a.txt", offset: "2", limit: null }, "read");
add("read: extra properties pass untouched", read, { path: "a.txt", extra: { n: "10" } }, "read");
add("read: path missing", read, { offset: 1 }, "read");
add("read: path a number", read, { path: 12 }, "read");
add("read: path a list", read, { path: ["a"] }, "read");
const edit = { type: "object", required: ["path", "edits"], properties: { path: { type: "string" }, edits: { type: "array", items: { type: "object", required: ["oldText", "newText"], properties: { oldText: { type: "string" }, newText: { type: "string" } } } } } };
add("edit: one edit", edit, { path: "f", edits: [{ oldText: "a", newText: "b" }] }, "edit");
add("edit: one edit as an object", edit, { path: "f", edits: { oldText: "a", newText: "b" } }, "edit");
add("edit: wrong types inside edits", edit, { path: "f", edits: [{ oldText: 1, newText: null }, { oldText: "x" }] }, "edit");
add("edit: edits a string", edit, { path: "f", edits: "x" }, "edit");

// unicode and lone surrogates
add("a string with a lone surrogate", value("string"), { value: "a\ud800b" });
add("a lone surrogate as a number", value("number"), { value: "\ud800" });
add("astral characters count once for minLength", value("string", { minLength: 2 }), { value: "😀" });
add("a lone surrogate counts once for maxLength", value("string", { maxLength: 1 }), { value: "\ud800A" });

// pi-schema keywords and references
add("a $ref that names nothing rejects only its value", { type: "object", properties: { t: { $ref: "#/definitions/Missing" } } }, { t: 1 });
add("a $ref that names nothing, absent", { type: "object", properties: { t: { $ref: "#/definitions/Missing" } } }, {});
add("an external $ref", { type: "object", properties: { t: { $ref: "https://example.com/schemas/t.json" } } }, { t: 1 });
add("definitions of draft 7", { $schema: "http://json-schema.org/draft-07/schema#", type: "object", required: ["t"], properties: { t: { $ref: "#/definitions/T" } }, definitions: { T: { type: "object", required: ["path"], properties: { path: { type: "string" } } } } }, { t: { path: 42 } });
add("a recursive reference", { type: "object", properties: { root: { $ref: "#/$defs/N" } }, $defs: { N: { type: "object", properties: { c: { type: "array", items: { $ref: "#/$defs/N" } }, v: { type: "integer" } } } } }, { root: { v: "2", c: [{ v: "3", c: [] }] } });
add("a recursive union", { type: "object", properties: { t: { $ref: "#/$defs/Tree" } }, $defs: { Tree: { anyOf: [{ type: "string" }, { type: "array", items: { $ref: "#/$defs/Tree" } }] } } }, { t: [5, ["a"]] });
add("the url format", value("string", { format: "url" }), { value: "not a url" });
add("the json-pointer-uri-fragment format", value("string", { format: "json-pointer-uri-fragment" }), { value: "#/a/b" });
add("an unknown format passes", value("string", { format: "made-up" }), { value: "x" });
add("an enum lists its values", value("string", { enum: ["a", "b", "c"] }), { value: "z" });
add("an enum of 30 values lists 20", value("string", { enum: Array.from({ length: 30 }, (_, i) => `v${i}`) }), { value: "z" });
add("oneOf matching two alternatives", value(undefined, { oneOf: [{ type: "string" }, { type: "string", maxLength: 5 }] }), { value: "abc" });
add("anyOf converts the first alternative that accepts", value(undefined, { anyOf: [{ type: "integer" }, { type: "boolean" }] }), { value: "3" });
add("patternProperties convert the matching members", { type: "object", patternProperties: { "^n_": { type: "integer" } } }, { n_a: "1", other: "2" });
add("additionalProperties convert only the undeclared members", { type: "object", properties: { amount: { type: "number" } }, additionalProperties: { type: "integer" } }, { amount: "3.5", extra: "7" });
add("property names are names, not patterns", { type: "object", properties: { "a.b": { type: "integer" }, axb: { type: "string" } } }, { "a.b": "4", axb: "5.8" });
add("a boolean schema", { type: "object", properties: { a: true, b: false } }, { b: 1 });
add("unknown keywords are ignored", { type: "object", "~kind": "Object", properties: { n: { "~kind": "Number", type: "number" } } }, { n: "5" });
add("an invalid pattern throws at compile", { type: "object", properties: { s: { type: "string", pattern: "[" } } }, { s: "y" });
add("a deep recursive union without exponential work", { type: "object", properties: { s: { $ref: "#/$defs/S" } }, $defs: { S: { anyOf: [{ type: "object", additionalProperties: { $ref: "#/$defs/S" }, required: ["a"] }, { type: "object", additionalProperties: { $ref: "#/$defs/S" }, required: ["b"] }] } } },
  { s: Array.from({ length: 40 }).reduce((v) => ({ c: v }), { x: 1 }) });

// root values
add("a root null", { type: "object", properties: {} }, null);
add("a root list", { type: "object", properties: {} }, []);
add("a root string", { type: "string" }, "x");
add("an empty schema", {}, { a: 1 });
// formatValidationPath (validation.ts:21-28) decodes `~1` before `~0`: the property "a~1" is the pointer /a~01, which reads back as "a~1"; decoding `~0` first would read it as "a/".
add("a property named a~1 is reported under its own name", { type: "object", properties: { "a~1": { type: "string" } } }, { "a~1": [1] });
add("a property named a/b is reported under its own name", { type: "object", properties: { "a/b": { type: "string" } } }, { "a/b": [1] });
add("a property named ~01 under a property named ~1", { type: "object", properties: { "~1": { type: "object", properties: { "~01": { type: "string" } } } } }, { "~1": { "~01": [1] } });
export { cases };
