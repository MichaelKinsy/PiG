#!/usr/bin/env node
// Upstream oracle for the lovely-mermaid Go port (internal/mermaid). Drives the
// lovely-mermaid@0.3.3 render() that Pi's coding-agent depends on so the Go port
// can be diffed byte-for-byte. Reads one JSON-quoted mermaid source per line
// (newlines survive JSON quoting) from the corpus in argv[2] and prints, per
// source, {input, kind, art} where art is null (render returned null) or
// {plain, width, warnings, classDefs, styled, ansi, ansiAlt}, where ansi / ansiAlt are
// toAnsi(art) with the default theme and with an all-roles theme. Regenerate goldens:
//   node test/parity/testdata/mermaid-pi.mjs internal/mermaid/testdata/corpus.txt | gzip -9n > internal/mermaid/testdata/render-golden.jsonl.gz
// See mermaid-oracle.mjs for how the package is located.
import { readFileSync } from "node:fs";
import { importLovely } from "./mermaid-oracle.mjs";

const { render, diagramKind, toAnsi, DEFAULT_THEME } = await importLovely("index.js");
// A second theme with every role set, as the Pi transformer builds one from its colours.
const ALT_THEME = { border: "38;5;240", text: "38;5;255", edge: "38;5;45", edgeLabel: "38;5;255", title: "1;38;5;45" };

const corpusPath = process.argv[2];
if (!corpusPath) {
	console.error("usage: mermaid-pi.mjs <corpus-file>");
	process.exit(2);
}

for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw);
	const art = render(input);
	process.stdout.write(
		JSON.stringify({
			input,
			kind: diagramKind(input),
			art: art === null ? null : {
				plain: art.plain,
				width: art.width,
				warnings: art.warnings,
				classDefs: art.classDefs,
				styled: art.styled,
				ansi: toAnsi(art, DEFAULT_THEME),
				ansiAlt: toAnsi(art, ALT_THEME),
			},
		}) + "\n",
	);
}
