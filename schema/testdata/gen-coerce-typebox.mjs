// Generates schema/testdata/coerce-typebox.json: the cases of Pi's packages/schema/test/coerce-typebox.test.ts (TypeBox 1.3.27's
// Value.Convert tests ported to pi-schema's conversion pass) as data: for every `it`, the JSON of the TypeBox schema, the input and
// the expectation of each assertion, in the test's describe/it names. A case whose schema or value has no JSON form (undefined,
// bigint, symbol) is listed with the kind that has none; Pi's own `it.skip` cases are listed as skipped.
// Run through Node 24 against a Pi export that holds packages/schema and node_modules/typebox:
//   node gen-coerce-typebox.mjs <pi-export-dir> <out.json> [<pi-commit>]
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [piRoot, outPath, commit = "b556cfe5"] = process.argv.slice(2);
const typeboxDir = join(piRoot, "node_modules/typebox");
const typeboxPackage = JSON.parse(readFileSync(join(typeboxDir, "package.json"), "utf8"));
const entry = typeboxPackage.exports["."];
const { Type } = await import(pathToFileURL(join(typeboxDir, typeof entry === "string" ? entry : (entry.import ?? entry.default))));

class NoJsonForm extends Error {
	constructor(kind) {
		super(kind);
		this.kind = kind;
	}
}

// JSON text of a value, with -0 kept and every value JSON cannot hold refused.
function enc(value) {
	switch (typeof value) {
		case "undefined":
			throw new NoJsonForm("undefined");
		case "bigint":
			throw new NoJsonForm("bigint");
		case "symbol":
			throw new NoJsonForm("symbol");
		case "function":
			throw new NoJsonForm("function");
		case "number":
			if (Object.is(value, -0)) return "-0";
			if (!Number.isFinite(value)) throw new NoJsonForm("non-finite number");
			return JSON.stringify(value);
		case "string":
		case "boolean":
			return JSON.stringify(value);
	}
	if (value === null) return "null";
	if (Array.isArray(value)) return `[${value.map(enc).join(",")}]`;
	if (Object.getPrototypeOf(value) !== Object.prototype) throw new NoJsonForm("non-plain object");
	return `{${Object.keys(value)
		.map((key) => `${JSON.stringify(key)}:${enc(value[key])}`)
		.join(",")}}`;
}

class Pending {
	constructor(schema, input) {
		this.schema = schema;
		this.input = input;
	}
}
class PendingCheck {
	constructor(pending, schema) {
		this.pending = pending;
		this.schema = schema;
	}
}

const groups = [];
let group;
let current;
const describe = (name, body) => {
	group = { describe: name, cases: [] };
	groups.push(group);
	body();
};
const it = (name, body) => {
	current = { name, assertions: [] };
	group.cases.push(current);
	body();
};
it.skip = (name) => {
	group.cases.push({ name, skipped: "Pi's it.skip: the schema or the value has no JSON form" });
};
const Value = {
	Convert: (schema, input) => new Pending(schema, input),
	Check: (schema, pending) => new PendingCheck(pending, schema),
};
const Assert = {
	IsEqual: (actual, expected) => {
		if (!(actual instanceof Pending)) throw new Error(`unexpected IsEqual in ${current.name}`);
		current.assertions.push({ schema: actual.schema, input: actual.input, expected });
	},
	IsTrue: (check) => {
		current.assertions.push({ schema: check.schema, input: check.pending.input, check: true, convertSchema: check.pending.schema });
	},
	IsFalse: (check) => {
		current.assertions.push({ schema: check.schema, input: check.pending.input, check: false, convertSchema: check.pending.schema });
	},
};
Object.assign(globalThis, { __tb: { Type }, __rec: { describe, it, Value, Assert } });

const source = readFileSync(join(piRoot, "packages/schema/test/coerce-typebox.test.ts"), "utf8");
const body = source.slice(source.indexOf('describe("Value.Convert.Any"'));
const shim = "const { Type } = (globalThis as any).__tb;\nconst { describe, it, Value, Assert } = (globalThis as any).__rec;\nconst Convert = Value.Convert;\n";
const dir = mkdtempSync(join(tmpdir(), "coerce-typebox-"));
const file = join(dir, "cases.mts");
writeFileSync(file, shim + body);
await import(pathToFileURL(file));

const out = [];
for (const g of groups) {
	const cases = g.cases.map((c) => {
		if (c.skipped !== undefined) return `{"name":${JSON.stringify(c.name)},"skipped":${JSON.stringify(c.skipped)}}`;
		try {
			const assertions = c.assertions.map((a) => {
				const parts = [`"schema":${enc(a.schema)}`, `"input":${enc(a.input)}`];
				if ("check" in a) {
					if (enc(a.convertSchema) !== enc(a.schema)) throw new Error(`Convert and Check schemas differ in ${c.name}`);
					parts.push(`"check":${a.check}`);
				} else parts.push(`"expected":${enc(a.expected)}`);
				return `{${parts.join(",")}}`;
			});
			return `{"name":${JSON.stringify(c.name)},"assertions":[${assertions.join(",")}]}`;
		} catch (error) {
			if (!(error instanceof NoJsonForm)) throw error;
			return `{"name":${JSON.stringify(c.name)},"noGoForm":${JSON.stringify(error.kind)}}`;
		}
	});
	out.push(`{"describe":${JSON.stringify(g.describe)},"cases":[\n${cases.join(",\n")}\n]}`);
}
writeFileSync(outPath, `{"piCommit":${JSON.stringify(commit)},"groups":[\n${out.join(",\n")}\n]}\n`);
