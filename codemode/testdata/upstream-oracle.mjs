// Generates upstream-golden.json: Pi's packages/codemode pure functions (declarations, identifier, source) run on
// deterministic corpus and seeded-random inputs. Run from the repo root with Node >= 24 (native type stripping):
//   node codemode/testdata/upstream-oracle.mjs > codemode/testdata/upstream-golden.json
// The upstream mirror is .upstream/current (override with PI_UPSTREAM). Schemas are carried as JSON text so the
// oracle and the Go test both parse the identical bytes.
import { writeSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const root = resolve(process.env.PI_UPSTREAM ?? ".upstream/current", "packages/codemode/src");
const load = (file) => import(pathToFileURL(resolve(root, file)).href);
const declarations = await load("declarations.ts");
const { toCodemodeIdentifier } = await load("identifier.ts");
const { parseCodemodeSource, CODEMODE_SOURCE_GRAMMAR, CodemodeSourceError } = await load("source.ts");
const prelude = await load("runtime/prelude-source.ts");

let seed = 0x9e3779b9;
function rnd(n) {
	seed ^= seed << 13;
	seed >>>= 0;
	seed ^= seed >>> 17;
	seed ^= seed << 5;
	seed >>>= 0;
	return seed % n;
}
const pick = (xs) => xs[rnd(xs.length)];

const NAMES = ["a", "b", "_c", "$d", "a-b", "1x", "", "z", "Z", "é", "😀", "\u{FF5E}", "\uFFFD", "a b", "constructor", "__proto__", "x\ny", 'q"uote', "ａ", "\u{10000}", "\uE000", "10", "2", "toString"];
const DESCS = ["", "  ", "plain", " padded ", "line1\nline2", "a\r\nb", "x */ y", "a\n\nb", "  \n ", "tab\tinside", "\u00a0nbsp\u00a0", "\u2028ls", "emoji 😀"];
const CONSTS = [0, -0, 1, 1.5, 1e21, 1e-7, -1e21, 123456789012345680000, "s", "é\u2028\u2029", "\u{1F600}", true, false, null, [1, "a"], { a: 1, b: [2] }, "a\"b\\c\n"];

function schema(depth) {
	const r = rnd(depth > 3 ? 8 : 24);
	switch (r) {
		case 0: return { type: "string" };
		case 1: return { type: "integer" };
		case 2: return { type: "number" };
		case 3: return { type: "boolean" };
		case 4: return { type: "null" };
		case 5: return pick([true, false]);
		case 6: return { const: pick(CONSTS) };
		case 7: return { enum: [pick(CONSTS), pick(CONSTS), pick(CONSTS)].slice(0, rnd(4)) };
		case 8: return { type: [pick(["string", "number", "null", "object", "array", "weird"]), pick(["string", "boolean", "integer"])] };
		case 9: return { anyOf: Array.from({ length: rnd(4) }, () => schema(depth + 1)) };
		case 10: return { oneOf: Array.from({ length: rnd(4) }, () => schema(depth + 1)) };
		case 11: return { allOf: Array.from({ length: rnd(4) }, () => schema(depth + 1)) };
		case 12: return { type: "array", items: schema(depth + 1) };
		case 13: return { type: "array", items: Array.from({ length: rnd(3) }, () => schema(depth + 1)) };
		case 14: return { type: "array", prefixItems: Array.from({ length: rnd(3) }, () => schema(depth + 1)) };
		case 15: return { items: schema(depth + 1) };
		case 16: return { type: "array" };
		case 17:
		case 18:
		case 19: {
			const properties = {};
			for (let i = rnd(5); i > 0; i--) {
				const p = schema(depth + 1);
				if (p !== null && typeof p === "object" && !Array.isArray(p) && rnd(3) === 0) p.description = pick(DESCS);
				properties[pick(NAMES)] = p;
			}
			const o = { type: "object", properties };
			if (rnd(2)) o.required = Array.from({ length: rnd(3) }, () => pick(NAMES));
			if (rnd(3) === 0) o.additionalProperties = pick([true, false, schema(depth + 1)]);
			if (rnd(4) === 0) delete o.type;
			return o;
		}
		case 20: return { $ref: pick(["#", "#/$defs/x", "#/definitions/y", "#/$defs/x/properties/a", "http://x/y", "#/%24defs/x", "#/constructor", "#/$defs/~0a", "#/", "#//$defs"]) };
		case 21: return { additionalProperties: schema(depth + 1) };
		case 22: return { required: ["a"] };
		default: return pick([{}, { type: "weird" }, { type: 5 }, "str", 5, null, []]);
	}
}
function root_() {
	const s = schema(0);
	if (typeof s === "object" && s !== null && !Array.isArray(s) && rnd(2)) {
		s.$defs = { x: schema(1), "~a": schema(1) };
		s.definitions = { y: schema(1) };
	}
	return s;
}
function mcpOutput() {
	const props = {
		content: pick([{ type: "array", items: { type: "object" } }, { type: "array", items: { type: "string" } }, { type: "string" }, true]),
		isError: pick([{ type: "boolean" }, { type: "boolean" }, { type: "string" }]),
		_meta: pick([{ type: "object" }, { type: "object" }, { type: "array" }]),
	};
	const sc = pick([undefined, schema(1), true, false, { type: "object" }, "x"]);
	if (sc !== undefined) props.structuredContent = sc;
	return { type: "object", properties: props, required: ["content"] };
}

const cases = { schemaToType: [], signature: [], sample: [], declarations: [], identifier: [], source: [] };
const call = (f) => {
	try {
		return { ok: f() };
	} catch (e) {
		return { error: e instanceof Error ? `${e.name}: ${e.message}` : String(e) };
	}
};

for (let i = 0; i < 350; i++) {
	const s = root_();
	const text = JSON.stringify(s) ?? "null";
	const maxChars = rnd(3) === 0 ? 1 + rnd(60) : undefined;
	cases.schemaToType.push({ schema: text, maxChars: maxChars ?? null, ...call(() => declarations.schemaToType(JSON.parse(text), maxChars === undefined ? {} : { maxChars })) });
}
// Raw JSON texts that JSON.stringify cannot produce: duplicate keys, __proto__ keys, escapes, big numbers.
for (const text of [
	'{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"},"a":{"type":"boolean"}}}',
	'{"type":"object","properties":{"__proto__":{"type":"string"},"b":{"type":"null"}}}',
	'{"type":"object","properties":{"constructor":{"type":"string"}},"required":["constructor"]}',
	'{"$ref":"#/__proto__"}',
	'{"$ref":"#/toString"}',
	'{"$ref":"#/%E0%A4%A"}', '{"$ref":"#/%FF","$defs":{"x":{}}}', '{"$ref":"#/$defs/%C3%A9","$defs":{"é":{"type":"string"}}}', '{"$ref":"#/%","$defs":{"x":{}}}', '{"$ref":"#/$defs/a+b","$defs":{"a+b":{"type":"null"}}}',
	'{"$defs":{"a/b":{"type":"string"}},"$ref":"#/$defs/a~1b"}',
	'{"const":1.0}', '{"const":1E2}', '{"const":-0}', '{"const":12345678901234567890}', '{"const":"\\ud800"}', '{"const":"\\u2028"}',
	'{"enum":[1.0,1,"1"]}', '{"enum":[{"b":1,"a":2},[]]}', '{"const":{"a":{"__proto__":1}}}',
	'{"type":"object","properties":{"\\ud83d\\ude00":{"type":"string"},"\\uffff":{"type":"string"}}}',
	'{"type":"object","properties":{"\\ud800":{"type":"string"},"a":{"type":"string"}}}',
	'{"type":"object","properties":{"10":{"type":"string"},"9":{"type":"string"},"a":{}}}',
	'{"type":"object","properties":{"a":{"description":"  x\\n\\n y \\r\\n z","type":"string"}}}',
	'{"type":"object","properties":{"a":{"description":"\\u00a0\\ufeff x \\u2028 y","type":"string"}}}',
	'{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"description":"d","type":"string"}}}}}',
	'{"type":"object","properties":{"a":{"description":"d","type":"object","properties":{"b":{"description":"e","type":"string"}}}}}',
	'{"type":"object","properties":{"a":5,"b":null,"c":[],"d":"s"}}',
	'{"allOf":[{"anyOf":[{"type":"string"},{"type":"number"}]},{"type":"boolean"},{}]}',
	'{"anyOf":[]}', '{"enum":[]}', '{"allOf":[]}', '{"type":[]}', '{"type":["string","string"]}', '{"anyOf":[true,{}]}',
	'{"anyOf":{"type":"string"},"oneOf":[{"type":"number"}]}', '{"oneOf":5,"type":"string"}',
	'{"enum":5,"type":"string"}', '{"const":null,"type":"string"}', '{"enum":null}', '{"properties":null}', '{"properties":[]}', '{"required":"a","properties":{"a":{}}}',
	'{"items":false}', '{"items":true}', '{"items":null,"prefixItems":[{}]}', '{"type":"array","items":[]}', '{"type":"array","prefixItems":[]}', '{"type":"array","items":[],"prefixItems":[{"type":"string"}]}',
]) {
	cases.schemaToType.push({ schema: text, maxChars: null, ...call(() => declarations.schemaToType(JSON.parse(text))) });
}
// A reference-expansion bomb: each level references the next one twice.
{
	const defs = {};
	for (let i = 0; i < 14; i++) defs[`n${i}`] = { type: "object", properties: { l: { $ref: `#/$defs/n${i + 1}` }, r: { $ref: `#/$defs/n${i + 1}` } } };
	defs.n14 = { type: "string" };
	const text = JSON.stringify({ $defs: defs, $ref: "#/$defs/n0" });
	cases.schemaToType.push({ schema: text, maxChars: null, ...call(() => declarations.schemaToType(JSON.parse(text))) });
	cases.schemaToType.push({ schema: text, maxChars: 100000000, ...call(() => declarations.schemaToType(JSON.parse(text), { maxChars: 100000000 })) });
}

const toolJSON = (tool) => JSON.stringify(tool);
for (let i = 0; i < 150; i++) {
	const tool = { name: pick(NAMES) + (rnd(2) ? pick(NAMES) : ""), description: pick(DESCS) };
	if (rnd(5)) tool.inputSchema = root_();
	if (rnd(3) === 0) tool.outputSchema = rnd(2) ? mcpOutput() : root_();
	const text = toolJSON(tool);
	const inputMaxChars = rnd(3) === 0 ? 1 + rnd(80) : undefined;
	const o = inputMaxChars === undefined ? {} : { inputMaxChars };
	cases.signature.push({ tool: text, inputMaxChars: inputMaxChars ?? null, ...call(() => declarations.renderToolSignature(JSON.parse(text), o)) });
	cases.sample.push({ tool: text, inputMaxChars: inputMaxChars ?? null, ...call(() => declarations.renderToolSample(JSON.parse(text), o)) });
}
for (let i = 0; i < 100; i++) {
	const mk = (global) => {
		const t = { name: pick(["a", "b", "c", "ns.x", "ns.y", "other.z", "tool", "mcp__s__t", "d-e"]), description: pick(DESCS) };
		if (rnd(3)) t.inputSchema = root_();
		if (rnd(3) === 0) t.outputSchema = root_();
		if (global && rnd(3) === 0) t.signature = pick(["(a: number): number", "()", ""]);
		return t;
	};
	const options = { tools: Array.from({ length: rnd(4) }, () => mk(false)), globals: Array.from({ length: rnd(5) }, () => mk(true)) };
	if (rnd(5) === 0) delete options.tools;
	if (rnd(5) === 0) delete options.globals;
	const text = JSON.stringify(options);
	cases.declarations.push({ options: text, ...call(() => declarations.renderDeclarations(JSON.parse(text))) });
}
for (const name of [...NAMES, "mcp__docs__search", "my-tool", "ab\u0301c", "\u{1F600}x", "x\u{1F600}", "\ud800", "a\ud800b", "9a", "$", "a.b", "日本語", "A\u0000B", "_9", " ", "\u212a", "İ"]) {
	cases.identifier.push({ name, ok: toCodemodeIdentifier(name) });
}
const SOURCES = [
	"", " ", "\n", "\u00a0\ufeff", "return 1", "x\ny", "// @options: {}", "// @options: {}\n", "// @options: {}\n  \n", "// @options: {}\nreturn 1",
	'// @options: {"max_output_tokens": 5}\nx', '// @options: {"timeout_ms": 5}\r\nx', '  \t// @options: {"timeout_ms": 2147483647}\nx', '// @options: {"timeout_ms": 2147483648}\nx',
	'// @options: {"timeout_ms": 0}\nx', '// @options: {"timeout_ms": 1.5}\nx', '// @options: {"timeout_ms": -1}\nx', '// @options: {"timeout_ms": 1e3}\nx', '// @options: {"timeout_ms": 5.0}\nx',
	'// @options: {"max_output_tokens": 9007199254740991}\nx', '// @options: {"max_output_tokens": 9007199254740992}\nx', '// @options: {"max_output_tokens": 0}\nx', '// @options: {"max_output_tokens": "5"}\nx', '// @options: {"max_output_tokens": null}\nx',
	'// @options: {"max_output_tokens": -0}\nx', '// @options: {"max_output_tokens": 1e0}\nx', '// @options: {"max_output_tokens": 5, "max_output_tokens": 6}\nx',
	'// @options: {"timeout_ms": 5, "max_output_tokens": 6}\nx', '// @options: {"foo": 1}\nx', '// @options: {"__proto__": 1}\nx', '// @options: {"max_output_tokens": 1, "foo":2, "bar":3}\nx',
	"// @options: [1]\nx", "// @options: null\nx", "// @options: 5\nx", '// @options: "s"\nx', "// @options: tru\nx", "// @options: {\nx", "// @options: {'a':1}\nx", '// @options: {"a":}\nx',
	'// @options: {"a":1,}\nx', '// @options: {"a":1} x\nx', "// @options: {}}\nx", "// @options: \u00a0{}\u00a0\nx", "// @options: \n x", "// @options:\nx", '// @options: {"a":01}\nx',
	'// @options: {"a":"\u0001"}\nx', '// @options: {"a":"\\x"}\nx', '// @options: {"a":1e}\nx', '// @options: {"a":-}\nx', '// @options: {"a":.5}\nx', '// @options: {"a":+1}\nx', "// @options: {a:1}\nx",
	'// @options: {"a" 1}\nx', '// @options: {"a":1 "b":2}\nx', "// @options: [\nx", "// @options: ]\nx", "// @options: {\"a\":\"b\nx", "// @options: nul\nx", "// @options: undefined\nx", "// @options: NaN\nx",
	"// @options: \u{1F600}\nx", "// @options: {\"\u{1F600}\":1}\nx", '// @options: {"a":"\u{1F600}', "// @options:{}\rx", "x\n// @options: {}\ny", "  // @options: {}", "// @options: {} \r\n  \r\n",
	"// @options: {\"timeout_ms\":1}\r\n\r\nx", "\ufeff// @options: {}\nx", "\n// @options: {}\nx", "//@options: {}\nx",
	'// @options: {"a":1}\n\n\n' + "x".repeat(40),
	'// @options: ' + '{"a":"' + "y".repeat(40) + '" 1}\nx', '// @options: ' + "[" + "1,".repeat(30) + "\nx",
	"// @options: truefalse\nx", "// @options: {\"a\":tru}\nx", '// @options: {"a":1}}\nx', "// @options: {\"a\":1}\u2028\nx",
];
for (const input of SOURCES) cases.source.push({ input, ...call(() => JSON.stringify(parseCodemodeSource(input))) });
for (let i = 0; i < 100; i++) {
	// Mutate valid option lines one character at a time.
	const base = '// @options: {"max_output_tokens": 12, "timeout_ms": 300}';
	const pos = rnd(base.length);
	const ch = pick(["", " ", "x", "\"", "{", "}", "[", ",", ":", "1", "-", ".", "e", "\\", "\u00e9", "\u{1F600}", "\t", "\u0000"]);
	const input = (rnd(2) ? base.slice(0, pos) + ch + base.slice(pos + 1) : base.slice(0, pos) + ch + base.slice(pos)) + "\ncode();";
	cases.source.push({ input, ...call(() => JSON.stringify(parseCodemodeSource(input))) });
}
// renderToolOutputType and mcpStructuredContentSchema, appended after every other case so the seeded cases above do not move.
cases.outputType = [];
for (const text of [
	"undefined", '{"type":"string"}', "true", "false", "null", "{}", '{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"isError":{"type":"boolean"},"_meta":{"type":"object"}}}',
	'{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"isError":{"type":"boolean"},"_meta":{"type":"object"},"structuredContent":{"type":"object","properties":{"n":{"type":"number"}}}}}',
	'{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"isError":{"type":"boolean"},"_meta":{"type":"object"},"structuredContent":{}}}',
	'{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"isError":{"type":"boolean"},"_meta":{"type":"object"},"structuredContent":true}}',
	'{"type":"object","properties":{"content":{"type":"array"}}}', '{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"isError":{"type":"string"},"_meta":{"type":"object"}}}',
	'{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"isError":{"type":"boolean"}}}', '{"type":"array","items":{"type":"string"}}', '{"$ref":"#/%E0%A4%A"}',
]) {
	const schema = text === "undefined" ? undefined : JSON.parse(text);
	cases.outputType.push({ schema: text, type: call(() => declarations.renderToolOutputType(schema)), structured: call(() => { const r = declarations.mcpStructuredContentSchema(schema); return r === undefined ? null : JSON.stringify(r); }) });
}
for (let i = 0; i < 80; i++) {
	const text = JSON.stringify(rnd(2) ? mcpOutput() : root_());
	const schema = JSON.parse(text);
	cases.outputType.push({ schema: text, type: call(() => declarations.renderToolOutputType(schema)), structured: call(() => { const r = declarations.mcpStructuredContentSchema(schema); return r === undefined ? null : JSON.stringify(r); }) });
}
// new CodemodeSourceError(message): the message, the name and Error's string form, appended after every other case.
cases.sourceError = ["", "x", "two\nlines", "\u{1F600} é", "@options must be a JSON object with supported fields `max_output_tokens` and `timeout_ms`"].map((message) => {
	const error = new CodemodeSourceError(message);
	return { message, name: error.name, text: String(error), isError: error instanceof Error };
});
cases.constants = {
	defaultInputSchemaMaxChars: declarations.DEFAULT_INPUT_SCHEMA_MAX_CHARS,
	mcpTypescriptPreamble: declarations.MCP_TYPESCRIPT_PREAMBLE,
	sourceGrammar: CODEMODE_SOURCE_GRAMMAR,
	maxStoreValueChars: prelude.MAX_STORE_VALUE_CHARS,
	maxStoreTotalChars: prelude.MAX_STORE_TOTAL_CHARS,
	maxOutputChars: prelude.MAX_OUTPUT_CHARS,
	maxOutputItems: prelude.MAX_OUTPUT_ITEMS,
};
writeSync(1, `${JSON.stringify(cases, null, "\t").replace(/\n\t\t\t/g, " ").replace(/\n\t\t\}/g, " }")}\n`);
