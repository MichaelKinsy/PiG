#!/usr/bin/env node
// Upstream oracle for the markdown+LaTeX integration. Drives pi's own Markdown
// component (pi-tui/dist/components/markdown.js) headless with an identity theme
// (we compare ANSI-stripped plain lines, so theme colors are irrelevant and the
// latex token emission is isolated). Reads one JSON-quoted markdown source per
// line from the corpus file in argv[2] and prints {input, lines} per source.
// Regenerate goldens:
//   node parity/testdata/markdown-latex-pi.mjs tui/testdata/markdown-latex-corpus.txt > tui/testdata/markdown-latex-golden.jsonl
import { homedir } from "node:os";
import { pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { join } from "node:path";

function version() {
	const m = readFileSync("coding/pigversion/pigversion.go", "utf8").match(/UpstreamVersion = "([^"]+)"/);
	if (!m) throw new Error("cannot read UpstreamVersion");
	return m[1];
}

const root = process.env.PI_PACKAGE_ROOT ?? join(
	homedir(),
	".local/share/mise/installs/npm-earendil-works-pi-coding-agent",
	version(),
	"lib/node_modules/@earendil-works/pi-coding-agent",
);
const tuiRoot = join(root, "node_modules/@earendil-works/pi-tui/dist");
const { Markdown } = await import(pathToFileURL(join(tuiRoot, "components/markdown.js")).href);

const identity = (s) => s;
const theme = {
	heading: identity, link: identity, linkUrl: identity, code: identity,
	codeBlock: identity, codeBlockBorder: identity, quote: identity, quoteBorder: identity,
	hr: identity, listBullet: identity, bold: identity, italic: identity,
	strikethrough: identity, underline: identity,
};

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))/g;
const strip = (s) => s.replace(ANSI, "");

const corpusPath = process.argv[2];
const width = Number(process.argv[3] ?? 80);
if (!corpusPath) {
	console.error("usage: markdown-latex-pi.mjs <corpus-file> [width]");
	process.exit(2);
}

for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw);
	const md = new Markdown(input, 0, 0, theme, undefined, {});
	const lines = md.render(width).map((line) => strip(line).replace(/\s+$/, ""));
	process.stdout.write(JSON.stringify({ input, lines }) + "\n");
}
