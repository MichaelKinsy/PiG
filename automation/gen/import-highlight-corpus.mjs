// Build tui/internal/hljs/testdata/corpus.json: the highlight.js 10.7.3 test inputs (test/markup and test/detect) plus
// PiG cases, the inputs of the highlight.js oracle that generate-highlight-oracle.mjs records from Pi.
//
// Usage: node import-highlight-corpus.mjs <highlight.js-10.7.3-checkout> <out-file>
// The checkout is https://github.com/highlightjs/highlight.js at tag 10.7.3; the npm package does not ship its tests.
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const [checkout, outFile] = process.argv.slice(2);
if (!checkout || !outFile) {
	console.error("usage: import-highlight-corpus.mjs <highlight.js-10.7.3-checkout> <out-file>");
	process.exit(2);
}
const expectedCommit = "0c4cc8a1c3aa373aee06796433c1389e4d2a3a45";
const commit = execFileSync("git", ["-C", checkout, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
if (commit !== expectedCommit) throw new Error(`highlight.js checkout is ${commit}, want 10.7.3 (${expectedCommit})`);

const cases = [];
for (const kind of ["markup", "detect"]) {
	const root = path.join(checkout, "test", kind);
	for (const language of fs.readdirSync(root).sort()) {
		const dir = path.join(root, language);
		if (!fs.statSync(dir).isDirectory()) continue;
		for (const file of fs.readdirSync(dir).sort()) {
			if (!file.endsWith(".txt") || file.endsWith(".expect.txt")) continue;
			cases.push({ id: `${kind}/${language}/${file}`, lang: language, code: fs.readFileSync(path.join(dir, file), "utf8") });
		}
	}
}

// PiG cases: the e2e-100 fenced blocks and codemode card, scopes that reach unusual theme formatters, auto-detected
// sublanguages, unsupported and empty languages, aliases, and text outside the Basic Multilingual Plane.
const pig = [
	["javascript", 'const [a] = await models.get("x", { key: 1, ok: true });\nfunction f(n) { return n * 2; } // twice'],
	["python", 'def f(n: int) -> int:\n    return {"key": n * 2, "ok": True}  # twice'],
	["go", 'func f(n int) map[string]any {\n\treturn map[string]any{"key": n * 2, "ok": true} // twice\n}'],
	["bash", 'for f in *.go; do echo "$f" | grep -q x && exit 1; done  # twice'],
	["json", '{"key": 1, "ok": true, "list": [null, "x"]}'],
	["javascript", 'const hits = await searchTools("read the contents of a file", { limit: 3 });\ntext("E2E:search:" + JSON.stringify(hits.map((t) => t.name)));\nconst all = ALL_TOOLS.map((t) => t.name).sort();'],
	["reasonml", "type t = Foo | Bar(int)\nlet x = Foo"],
	["markdown", "# T\n*em* **strong** [l](u)\n"],
	["diff", "-old\n+new\n"],
	["html", "<div></div>"],
	["", "plain"],
	["klingon", "x"],
	["JS", "let x = 1"],
	["http", 'POST / HTTP/1.1\nContent-Type: application/json\n\n{"a": 1}'],
	["http", "HTTP/1.1 200 OK\n\n<html><body>hi</body></html>"],
	["javascript", "a 😀 é 𝒳 b"],
	["cpp", 'auto s = R"x(raw ) " text)x";'],
	["ruby", "x = <<~EOS\n  body\nEOS\n"],
	["typescript", "const a = <T,>(x: T) => <div>{x}</div>;\nconst b: Array<Array<number>> = [];"],
	["r", "x <- function(a) { a %>% f() }"],
	["mathematica", "Plot[Sin[x], {x, 0, Pi}] + myFunc[1]"],
	["sql", "SELECT \u212Aey FROM t WHERE ſ = 1"],
];
for (const [lang, code] of pig) cases.push({ id: `pig/${cases.length}`, lang, code });
fs.writeFileSync(outFile, `${JSON.stringify(cases, null, "\t")}\n`);
console.log(`${cases.length} cases`);
