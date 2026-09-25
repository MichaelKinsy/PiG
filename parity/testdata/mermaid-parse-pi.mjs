#!/usr/bin/env node
// Upstream oracle for the mermaid parser (internal/mermaid parse.go). Drives
// grok-mermaid@0.2.2's own exported parse functions from the pinned pi dist and
// dumps the parsed model per source, so the Go port can be diffed structurally
// before layout lands. model is null when render would fall back (unknown kind,
// syntax error, over-cap, empty). Reads one JSON-quoted source per line from
// argv[2].
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
const P = await import(pathToFileURL(join(root, "node_modules/grok-mermaid/dist/parse.js")).href);

function graphModel(g) {
	return { nodes: g.nodes, edges: g.edges, dir: g.dir, groups: g.groups, warnings: g.warnings };
}

function model(src) {
	const kind = P.diagramKind(src);
	if (kind === "flowchart") { const g = P.parseGraph(src); return g ? graphModel(g) : null; }
	if (kind === "state") { const g = P.parseState(src); return g ? graphModel(g) : null; }
	if (kind === "class") { const r = P.parseClass(src); return r ? { ...graphModel(r.graph), infos: r.infos } : null; }
	if (kind === "er") { const r = P.parseEr(src); return r ? { ...graphModel(r.graph), infos: r.infos } : null; }
	if (kind === "sequence") { const s = P.parseSequence(src); return s ? { labels: s.labels, items: s.items } : null; }
	return null;
}

const corpusPath = process.argv[2];
if (!corpusPath) { console.error("usage: mermaid-parse-pi.mjs <corpus>"); process.exit(2); }
for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw);
	process.stdout.write(JSON.stringify({ input, kind: P.diagramKind(input), model: model(input) }) + "\n");
}
