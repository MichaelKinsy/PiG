// Record Pi's syntax highlighting of tui/internal/hljs/testdata/corpus.json for the Go port's oracle test.
//
// The script replays one call sequence in a fresh Pi module instance, because highlight.js compiles and mutates its
// grammars as it highlights. For each corpus case, first with the eager languages and again after
// loadAllHighlightLanguages, it records supportsLanguage, the hljs.highlight HTML, theme.ts highlightCode and
// getMarkdownTheme().highlightCode with the dark theme in truecolor. It then highlights mutated detect samples in every
// language. Outputs are recorded as SHA-256 prefixes of the bytes Node writes: WTF-8 HTML and UTF-8 terminal text.
//
// Usage: node generate-highlight-oracle.mjs <pi-package-root> <corpus.json> <out-file>
import crypto from "node:crypto";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";
import zlib from "node:zlib";

const [packageArg, corpusFile, outFile] = process.argv.slice(2);
if (!packageArg || !corpusFile || !outFile) {
	console.error("usage: generate-highlight-oracle.mjs <pi-package-root> <corpus.json> <out-file>");
	process.exit(2);
}
// Pi's chalk decorations (emphasis, strong, link) appear only with color support, as in an interactive terminal.
process.env.FORCE_COLOR = "3";
const packageRoot = path.resolve(packageArg);
const require = createRequire(path.join(packageRoot, "package.json"));
const hljs = require("highlight.js/lib/core.js");
const importDist = (file) => import(pathToFileURL(path.join(packageRoot, file)).href);
const tui = await importDist("node_modules/@earendil-works/pi-tui/dist/index.js");
const themeModule = await importDist("dist/modes/interactive/theme/theme.js");
const syntaxHighlight = await importDist("dist/utils/syntax-highlight.js");
tui.setCapabilities({ images: null, trueColor: true, hyperlinks: false });
themeModule.initTheme("dark");
const markdownTheme = themeModule.getMarkdownTheme();

// WTF-8: UTF-8 that keeps a lone surrogate as its own three-byte unit, as Go strings in the port carry it.
const wtf8 = (text) => {
	const bytes = [];
	for (let i = 0; i < text.length; i++) {
		let c = text.charCodeAt(i);
		if (c >= 0xd800 && c <= 0xdbff && i + 1 < text.length) {
			const d = text.charCodeAt(i + 1);
			if (d >= 0xdc00 && d <= 0xdfff) {
				c = 0x10000 + ((c - 0xd800) << 10) + (d - 0xdc00);
				i++;
			}
		}
		if (c < 0x80) bytes.push(c);
		else if (c < 0x800) bytes.push(0xc0 | (c >> 6), 0x80 | (c & 63));
		else if (c < 0x10000) bytes.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
		else bytes.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
	}
	return Buffer.from(bytes);
};
const digest = (bytes) => crypto.createHash("sha256").update(bytes).digest("hex").slice(0, 16);
const htmlDigest = (code, language) => {
	try {
		return digest(wtf8(hljs.highlight(code, { language, ignoreIllegals: true }).value));
	} catch (error) {
		return digest(wtf8(`THROW:${error.message}`));
	}
};
const linesDigest = (lines) => digest(Buffer.from(lines.join("\n"), "utf8"));

// The mutations the Go test applies to the same detect samples (hljs_oracle_test.go mutate).
const mutate = (code, mutation) => {
	switch (mutation) {
		case 0:
			return code;
		case 1:
			return code.replace(/\n/g, "\r\n");
		case 2:
			return code.replace(/e/g, "é😀").replace(/K/g, "\u212A");
		case 3:
			return code.replace(/[a-z]/g, (c) => c.toUpperCase());
		case 4:
			return code.replace(/a/g, "\u2028").replace(/\./g, "\uD83D");
		default: {
			const middle = Math.floor(code.length / 2);
			return `${code.slice(0, middle)}\u017F\u0130\u017F${code.slice(middle)}`;
		}
	}
};

const corpus = JSON.parse(fs.readFileSync(corpusFile, "utf8"));
const cases = [];
const record = (stage) => {
	for (const [index, { lang, code }] of corpus.entries()) {
		const supported = Boolean(lang) && syntaxHighlight.supportsLanguage(lang);
		cases.push({
			stage,
			case: index,
			supported,
			html: supported ? htmlDigest(code, lang) : "",
			lines: linesDigest(themeModule.highlightCode(code, lang)),
			markdown: linesDigest(markdownTheme.highlightCode(code, lang)),
		});
	}
};
record("eager");
await syntaxHighlight.loadAllHighlightLanguages();
record("all");

const languages = hljs.listLanguages();
const detect = corpus.map((entry, index) => ({ ...entry, index })).filter((entry) => entry.id.startsWith("detect/"));
const mutations = [];
let k = 0;
for (const language of languages) {
	for (const sample of detect) {
		k++;
		if (k % 11 !== 0) continue;
		mutations.push({ lang: language, case: sample.index, mutation: k % 6, html: htmlDigest(mutate(sample.code, k % 6), language) });
	}
}

const oracle = { pi: JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8")).version, languages, cases, mutations };
fs.writeFileSync(outFile, zlib.gzipSync(Buffer.from(`${JSON.stringify(oracle)}\n`), { level: 9 }));
console.log(`${cases.length} cases and ${mutations.length} mutated samples`);
