#!/usr/bin/env node
// Upstream oracle for the label functions (internal/mermaid labels.go). Drives
// grok-mermaid@0.2.2's own exported label helpers from the pinned pi dist so the
// Go port can be diffed directly. Reads one JSON-quoted string per line from the
// corpus in argv[2] and prints per-input results of each helper.
import { homedir } from "node:os";
import { pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { join } from "node:path";

function version() {
	const m = readFileSync("coding/pigversion/pigversion.go", "utf8").match(/UpstreamVersion = "([^"]+)"/);
	if (!m) throw new Error("cannot read UpstreamVersion");
	return m[1];
}
const root = process.env.PI_PACKAGE_ROOT ?? join(homedir(), ".local/share/mise/installs/npm-earendil-works-pi-coding-agent", version(),
	"lib/node_modules/@earendil-works/pi-coding-agent");
const L = await import(pathToFileURL(join(root, "node_modules/grok-mermaid/dist/labels.js")).href);

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
		srcLines: L.srcLines(input),
	}) + "\n");
}
