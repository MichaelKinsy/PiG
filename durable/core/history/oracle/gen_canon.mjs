// Oracle for the JSON encoder: for random JSON texts (including non-canonical forms), the expected text is
// JSON.stringify(JSON.parse(text)). Output: testdata/canon.hex, one "hex(input) hex(output)" pair per line, output
// "-" when JSON.parse rejects the input.
import { writeFileSync } from "node:fs";
import { rng, hex } from "./rng.mjs";

const r = rng(20261006);
const strPieces = ["a", "b", "x y", "é", "日本", "😀", "\\n", "\\t", "\\\"", "\\\\", "\\/", "\\u0041", "\\u00e9", "\\ud83d\\ude00", "\\ud800", "\\udc00", "\\udbff", "\\u2028", "\u2028", "\u2029", "\\u0000", "\\u001f", "\\b", "\\f", "\\r", "\x7f", "<>&", "'"];
const keyPool = ["id", "kind", "model", "role", "0", "1", "2", "10", "4294967294", "4294967295", "01", "-1", "1.5", "a", "b", "__proto__", "constructor", "x", "é", "\\u0030", "\\u0031"];
const numPool = ["0", "-0", "1", "-1", "12", "123456789012345", "1234567890123456", "9007199254740993", "1.5", "-2.25", "0.1", "0.000001", "0.0000001", "1e21", "1e20", "1E5", "1e-7", "1.7976931348623157e308", "5e-324", "123456789012345678901", "100000000000000000000", "1e+3", "0.5e1", "1.0", "1.10", "1e0", "-0.0", "4.35", "0.30000000000000004", "123e-20", "1.2345e-6", "2e-7"];
const ws = ["", "", "", " ", "\n", "\t ", "\r\n"];

function str() {
	let s = "";
	for (let i = r.int(5); i > 0; i--) s += r.pick(strPieces);
	return `"${s}"`;
}
function val(d) {
	const c = r.int(d > 4 ? 5 : 8);
	if (c === 0) return str();
	if (c === 1) return r.pick(numPool);
	if (c === 2) return r.pick(["true", "false", "null"]);
	if (c === 3 || c === 4) return str();
	if (c === 5 || c === 6) {
		const n = r.int(6);
		const fs = [];
		for (let i = 0; i < n; i++) fs.push(`${r.pick(ws)}"${r.pick(keyPool)}"${r.pick(ws)}:${r.pick(ws)}${val(d + 1)}${r.pick(ws)}`);
		return `{${fs.join(",")}}`;
	}
	const n = r.int(5);
	const es = [];
	for (let i = 0; i < n; i++) es.push(`${r.pick(ws)}${val(d + 1)}${r.pick(ws)}`);
	return `[${es.join(",")}]`;
}

const lines = [];
const bad = ["", "{", "[1,]", "{\"a\":1,}", "01", "1.", ".5", "+1", "\"\\x\"", "\"\n\"", "tru", "nul", "[1 2]", "{\"a\" 1}", "{a:1}", "'a'", "\"\\u12\"", "1e", "--1", "[", "]", "{}}", "1 2", "\"abc", "NaN", "Infinity"];
for (const b of bad) lines.push(`${hex(b)} -`);
for (let i = 0; i < 2500; i++) {
	const t = `${r.pick(ws)}${val(0)}${r.pick(ws)}`;
	let out;
	try {
		out = JSON.stringify(JSON.parse(t));
		if (out === undefined) out = "-";
		else if (/(^|[^\\])(null)/.test("")) out = out;
	} catch {
		out = "-";
	}
	lines.push(`${hex(t)} ${out === "-" ? "-" : hex(out)}`);
}
writeFileSync(new URL("../testdata/canon.hex", import.meta.url), lines.join("\n") + "\n");
console.log(lines.length, "cases");
