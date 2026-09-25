#!/usr/bin/env node
// Upstream oracle for the grok-mermaid Go port (internal/mermaid). Drives pi's
// own bundled grok-mermaid@0.2.2 render() from the pinned pi dist so the Go port
// can be diffed byte-for-byte. Reads one JSON-quoted mermaid source per line
// (newlines survive JSON quoting) from the corpus in argv[2] and prints, per
// source, {input, kind, art} where art is null (render returned null) or
// {plain, width, warnings, styled}. Regenerate goldens:
//   node parity/testdata/mermaid-pi.mjs internal/mermaid/testdata/corpus.txt > internal/mermaid/testdata/golden.jsonl
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
const gm = join(root, "node_modules/grok-mermaid/dist/index.js");
const { render, diagramKind } = await import(pathToFileURL(gm).href);

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
				styled: art.styled,
			},
		}) + "\n",
	);
}
