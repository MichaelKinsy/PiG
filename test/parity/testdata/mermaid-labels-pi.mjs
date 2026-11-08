#!/usr/bin/env node
// Upstream oracle for the label functions (internal/mermaid labels.go). Drives
// lovely-mermaid@0.3.3's own exported label helpers so the
// Go port can be diffed directly. Reads one JSON-quoted string per line from the
// corpus in argv[2] and prints per-input results of each helper.
import { readFileSync } from "node:fs";
import { importLovely } from "./mermaid-oracle.mjs";

const L = await importLovely("labels.js");

const corpusPath = process.argv[2];
if (!corpusPath) { console.error("usage: mermaid-labels-pi.mjs <corpus>"); process.exit(2); }

for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw);
	process.stdout.write(JSON.stringify({
		input,
		clean: L.cleanLabel(input),
		htmlTags: L.stripHtmlTags(input),
		markdown: L.stripMarkdown(input),
		entities: L.decodeHtmlEntities(input),
		lower: L.asciiLower(input),
		upper: L.asciiUpper(input),
		wrap: L.wrapLabel(input, 24, 4),
		fit: L.fitLabel(input, 28),
		generics: L.displayGenerics(input),
		srcLines: L.srcLines(input),
	}) + "\n");
}
