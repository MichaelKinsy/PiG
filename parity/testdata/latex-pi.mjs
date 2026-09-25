#!/usr/bin/env node
// Upstream oracle for the LaTeX -> Unicode port (internal/latex). Runs pi's own
// renderLatex from the pinned pi-tui dist so the Go port can be diffed against it
// byte-for-byte. Reads one LaTeX expression per line from the corpus file given
// as argv[2] (blank/`#`-prefixed lines are skipped) and prints one JSON object
// per input: {input, inline, display}, where a value is null when renderLatex
// returned undefined. Regenerate goldens with:
//   node parity/testdata/latex-pi.mjs internal/latex/testdata/corpus.txt > internal/latex/testdata/golden.jsonl
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
const latexJs = join(root, "node_modules/@earendil-works/pi-tui/dist/latex.js");
const { renderLatex } = await import(pathToFileURL(latexJs).href);

const corpusPath = process.argv[2];
if (!corpusPath) {
	console.error("usage: latex-pi.mjs <corpus-file>");
	process.exit(2);
}

const orUndef = (v) => (v === undefined ? null : v);
for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw); // corpus lines are JSON-quoted so whitespace/escapes survive
	process.stdout.write(
		JSON.stringify({
			input,
			inline: orUndef(renderLatex(input)),
			display: orUndef(renderLatex(input, { display: true })),
		}) + "\n",
	);
}
