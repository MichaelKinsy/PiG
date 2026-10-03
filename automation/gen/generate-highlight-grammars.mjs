// Snapshot the highlight.js language graph that Pi's utils/syntax-highlight.ts registers, for the Go port in tui/internal/hljs.
//
// The snapshot is taken from the exact published Pi package: Pi's own module registers its eager languages, then Pi's
// loadAllHighlightLanguages() registers the complete set. Every registration is recorded in order, before any code is
// highlighted, so the Go registry replays registerLanguage exactly. Object identity is preserved: highlight.js mutates
// mode objects while it compiles them (isCompiled, cachedVariants, deleting keywords.$pattern), and some modes are shared
// between modes and between the two registrations of a language.
//
// Usage: node generate-highlight-grammars.mjs <pi-package-root> <out-dir>
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";
import zlib from "node:zlib";

const [packageArg, outDir] = process.argv.slice(2);
const packageRoot = packageArg && path.resolve(packageArg);
if (!packageArg || !outDir) {
	console.error("usage: generate-highlight-grammars.mjs <pi-package-root> <out-dir>");
	process.exit(2);
}
const require = createRequire(path.join(packageRoot, "package.json"));
const hljsPackage = JSON.parse(fs.readFileSync(require.resolve("highlight.js/package.json"), "utf8"));
const hljs = require("highlight.js/lib/core.js");

// Every registerLanguage call, in order, with the registered language object.
const registrations = [];
const registerLanguage = hljs.registerLanguage;
hljs.registerLanguage = function (name, definition) {
	registerLanguage.call(this, name, definition);
	registrations.push([name, hljs.getLanguage(name)]);
};
const syntaxHighlight = await import(pathToFileURL(path.join(packageRoot, "dist/utils/syntax-highlight.js")).href);
const eagerCount = registrations.length;
if (eagerCount === 0) throw new Error("Pi registered no eager highlight.js languages");
await syntaxHighlight.loadAllHighlightLanguages();
if (registrations.length === eagerCount) throw new Error("loadAllHighlightLanguages registered no languages");

// Language-definition callbacks. Each is ported by name in tui/internal/hljs/callbacks.go; an unknown callback fails generation.
const normalize = (source) => source.replace(/\s+/g, " ").trim();
const callbacks = new Map(
	[
		["endSameAsBegin.begin", "(m, resp) => { resp.data._beginMatch = m[1]; }"],
		["endSameAsBegin.end", "(m, resp) => { if (resp.data._beginMatch !== m[1]) resp.ignoreMatch(); }"],
		["shebang.begin", "(m, resp) => { if (m.index !== 0) resp.ignoreMatch(); }"],
		[
			"javascript.isTrulyOpeningTag",
			'(match, response) => { const afterMatchIndex = match[0].length + match.index; const nextChar = match.input[afterMatchIndex]; // nested type? // HTML should not include another raw `<` inside a tag // But a type might: `<Array<Array<number>>`, etc. if (nextChar === "<") { response.ignoreMatch(); return; } // <something> // This is now either a tag or a type. if (nextChar === ">") { // if we cannot find a matching closing tag, then we // will ignore it if (!hasClosingTag(match, { after: afterMatchIndex })) { response.ignoreMatch(); } } }',
		],
		["mathematica.systemSymbol", "(match, response) => { if (!SYSTEM_SYMBOLS_SET.has(match[0])) response.ignoreMatch(); }"],
		[
			"r.beforeMatch",
			'(mode, parent) => { if (!mode.beforeMatch) return; // starts conflicts with endsParent which we need to make sure the child // rule is not matched multiple times if (mode.starts) throw new Error("beforeMatch cannot be used with starts"); const originalMode = Object.assign({}, mode); Object.keys(mode).forEach((key) => { delete mode[key]; }); mode.begin = concat(originalMode.beforeMatch, lookahead(originalMode.begin)); mode.starts = { relevance: 0, contains: [ Object.assign(originalMode, { endsParent: true }) ] }; mode.relevance = 0; delete originalMode.beforeMatch; }',
		],
	].map(([name, source]) => [normalize(source), name]),
);
const usedCallbacks = new Set();

// The javascript callback closes over hasClosingTag; the Go port reimplements it, so its source is pinned too.
for (const language of ["javascript", "typescript"]) {
	const source = fs.readFileSync(require.resolve(`highlight.js/lib/languages/${language}.js`), "utf8");
	const hasClosingTag = source.match(/const hasClosingTag = \(match, \{ after \}\) => \{[\s\S]*?\n {2}\};/);
	if (
		!hasClosingTag ||
		normalize(hasClosingTag[0]) !==
			normalize(`const hasClosingTag = (match, { after }) => {
    const tag = "</" + match[0].slice(1);
    const pos = match.input.indexOf(tag, after);
    return pos !== -1;
  };`)
	) {
		throw new Error(`${language}: hasClosingTag changed; update callbacks.go and this generator`);
	}
}

// The mathematica callback closes over the module-level SYSTEM_SYMBOLS list.
const mathematicaPath = require.resolve("highlight.js/lib/languages/mathematica.js");
const systemSymbols = new Function("module", "exports", `${fs.readFileSync(mathematicaPath, "utf8")}\nreturn SYSTEM_SYMBOLS;`)(
	{ exports: {} },
	{},
);
if (!Array.isArray(systemSymbols) || systemSymbols.some((symbol) => typeof symbol !== "string")) {
	throw new Error("mathematica SYSTEM_SYMBOLS is not a string list");
}

// Keys the highlight.js 10.7.3 compiler and highlighter read from a mode or a language. Other keys are inert grammar data.
const modeKeys = new Set([
	"begin",
	"end",
	"match",
	"illegal",
	"lexemes",
	"beforeMatch",
	"className",
	"contains",
	"variants",
	"starts",
	"endsParent",
	"endsWithParent",
	"excludeBegin",
	"excludeEnd",
	"returnBegin",
	"returnEnd",
	"skip",
	"endSameAsBegin",
	"relevance",
	"keywords",
	"beginKeywords",
	"subLanguage",
	"on:begin",
	"on:end",
	"case_insensitive",
	"name",
	"aliases",
	"disableAutodetect",
	"supersetOf",
	"classNameAliases",
	"compilerExtensions",
]);
const compiledKeys = new Set(["isCompiled", "cachedVariants", "data", "matcher", "keywordPatternRe", "terminatorEnd", "__beforeBegin"]);

const assertWellFormed = (text, where) => {
	if (!text.isWellFormed()) throw new Error(`${where}: string has a lone surrogate`);
	return text;
};

const ids = new Map();
const roles = new Map();
const nodes = [];
const queue = [];
const visit = (object, role, where) => {
	if (ids.has(object)) {
		if (roles.get(object) !== role) throw new Error(`${where}: object used as ${roles.get(object)} and ${role}`);
		return ids.get(object);
	}
	const id = nodes.length;
	ids.set(object, id);
	roles.set(object, role);
	nodes.push(null);
	queue.push([object, id, role, where]);
	return id;
};

const encodeValue = (value, where) => {
	if (value === null || typeof value === "boolean") return value;
	if (typeof value === "number") {
		if (!Number.isFinite(value)) throw new Error(`${where}: non-finite number`);
		return value;
	}
	if (typeof value === "string") return assertWellFormed(value, where);
	if (value instanceof RegExp) {
		if (value.flags.includes("u") || value.flags.includes("v")) throw new Error(`${where}: unicode RegExp is not ported`);
		return { re: assertWellFormed(value.source, where) };
	}
	if (typeof value === "function") {
		const name = callbacks.get(normalize(value.toString()));
		if (!name) throw new Error(`${where}: unknown callback ${value.toString().slice(0, 200)}`);
		usedCallbacks.add(name);
		return { fn: name };
	}
	if (Array.isArray(value)) return { arr: value.map((item, index) => encodeValue(item, `${where}[${index}]`)) };
	throw new Error(`${where}: unexpected value`);
};

const encodeNode = (object, role, where) => {
	const props = [];
	for (const key of Object.keys(object)) {
		if (role === "mode" && !modeKeys.has(key)) continue;
		if (compiledKeys.has(key)) throw new Error(`${where}.${key}: compiled state before highlighting`);
		const value = object[key];
		const at = `${where}.${key}`;
		if (value === undefined) {
			props.push([key, { undef: true }]);
			continue;
		}
		if (role === "mode" && (key === "contains" || key === "variants") && value !== null) {
			if (!Array.isArray(value)) throw new Error(`${at}: not an array`);
			props.push([key, { arr: value.map((item, index) => (item === "self" ? "self" : { ref: visit(item, "mode", `${at}[${index}]`) })) }]);
			continue;
		}
		if (role === "mode" && key === "starts") {
			props.push([key, value === null ? null : { ref: visit(value, "mode", at) }]);
			continue;
		}
		if (role === "mode" && key === "keywords" && typeof value === "object" && value !== null && !Array.isArray(value)) {
			props.push([key, { ref: visit(value, "keywords", at) }]);
			continue;
		}
		if (role === "mode" && key === "classNameAliases") {
			props.push([key, { ref: visit(value, "aliases", at) }]);
			continue;
		}
		props.push([key, encodeValue(value, at)]);
	}
	const node = { k: role, p: props };
	if (Object.isFrozen(object)) node.frozen = true;
	return node;
};

const drain = () => {
	while (queue.length > 0) {
		const [object, id, role, where] = queue.shift();
		nodes[id] = encodeNode(object, role, where);
	}
};

// Eager-reachable nodes come first so the Go registry decodes only them until loadAllHighlightLanguages runs.
const encodeRegistrations = (list) =>
	list.map(([name, language]) => {
		assertWellFormed(name, "language name");
		const id = visit(language, "mode", name);
		drain();
		return [name, id];
	});
const eager = encodeRegistrations(registrations.slice(0, eagerCount));
const eagerNodeCount = nodes.length;
const all = encodeRegistrations(registrations.slice(eagerCount));

for (const name of callbacks.values()) {
	if (!usedCallbacks.has(name)) throw new Error(`callback ${name} is no longer used; remove its Go port`);
}

// Case-insensitive languages lowercase keywords and lexemes with toLowerCase. The Go port maps each code point through the
// table below; only Final_Sigma is context-dependent, so no keyword may contain a sigma.
for (const node of nodes) {
	if (node.k !== "keywords") continue;
	for (const [key, value] of node.p) {
		if (key === "$pattern") continue;
		const words = typeof value === "string" ? [value] : value && value.arr ? value.arr : [];
		for (const word of words) {
			if (typeof word === "string" && /[\u03a3\u03c2\u03c3]/.test(word)) throw new Error(`keyword ${key} has a sigma: ${word}`);
		}
	}
}

// RegExp ignoreCase canonicalization (ECMA-262 Canonicalize, non-unicode): the code units whose canonical form differs.
const canonicalize = [];
for (let unit = 0; unit <= 0xffff; unit++) {
	const upper = String.fromCharCode(unit).toUpperCase();
	if (upper.length !== 1) continue;
	const canonical = upper.charCodeAt(0);
	if (unit >= 128 && canonical < 128) continue;
	if (canonical !== unit) canonicalize.push([unit, canonical]);
}
// String.prototype.toLowerCase without context: every code point whose lowercase form differs.
const lowerCase = [];
for (let codePoint = 0; codePoint <= 0x10ffff; codePoint++) {
	if (codePoint >= 0xd800 && codePoint <= 0xdfff) continue;
	const text = String.fromCodePoint(codePoint);
	const lower = text.toLowerCase();
	if (lower !== text) lowerCase.push([codePoint, lower]);
}

const write = (name, value) => {
	const file = path.join(outDir, name);
	fs.writeFileSync(file, zlib.gzipSync(Buffer.from(`${JSON.stringify(value)}\n`), { level: 9 }));
};
fs.mkdirSync(outDir, { recursive: true });
write("grammars_eager.json.gz", {
	highlightjs: hljsPackage.version,
	nodes: nodes.slice(0, eagerNodeCount),
	registrations: eager,
	systemSymbols,
	canonicalize,
	lowerCase,
});
write("grammars_all.json.gz", { firstNode: eagerNodeCount, nodes: nodes.slice(eagerNodeCount), registrations: all });
console.log(
	`highlight.js ${hljsPackage.version}: ${eager.length} eager and ${all.length} deferred registrations, ${eagerNodeCount} + ${nodes.length - eagerNodeCount} grammar nodes`,
);
