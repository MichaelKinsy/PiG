#!/usr/bin/env node
// Upstream oracle for the mermaid parsers (internal/mermaid flowchart.go,
// state.go, class.go, er.go, sequence.go). Drives lovely-mermaid@0.3.3's own
// exported parse functions and dumps the parsed model per source, so the Go port
// can be diffed structurally before layout lands. model is null when render
// would fall back (unknown kind, nothing parsed, empty). Diagram kinds without an
// exported parser (pie, mindmap, timeline, gitGraph) are covered by the render
// oracle only. Reads one JSON-quoted source per line from argv[2].
import { readFileSync } from "node:fs";
import { importLovely } from "./mermaid-oracle.mjs";

const { diagramKind } = await importLovely("index.js");
const { parseGraph } = await importLovely("diagrams/flowchart.js");
const { parseState } = await importLovely("diagrams/state.js");
const { parseClass } = await importLovely("diagrams/class.js");
const { parseEr } = await importLovely("diagrams/er.js");
const { parseSequence } = await importLovely("diagrams/sequence.js");
const { stripControls } = await importLovely("labels.js");

function graphModel(g) {
	return {
		nodes: g.nodes, edges: g.edges, dir: g.dir, groups: g.groups, nodeGroup: g.nodeGroup,
		warnings: g.warnings, classDefs: g.classDefs, truncated: g.truncated,
	};
}

function model(src) {
	const kind = diagramKind(src);
	const g = (parse) => { const r = parse(src); return r ? graphModel(r) : null; };
	if (kind === "flowchart") return g(parseGraph);
	if (kind === "state") return g(parseState);
	if (kind === "class") return g(parseClass);
	if (kind === "er") return g(parseEr);
	if (kind === "sequence") {
		const s = parseSequence(src);
		return s ? { labels: s.labels, items: s.items, activations: s.activations, warnings: s.warnings, truncated: s.truncated } : null;
	}
	return null;
}

const corpusPath = process.argv[2];
if (!corpusPath) { console.error("usage: mermaid-parse-pi.mjs <corpus>"); process.exit(2); }
for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw);
	// render() strips control characters before dispatching; the parsers are
	// called on the stripped source the same way.
	const src = stripControls(input);
	process.stdout.write(JSON.stringify({ input, kind: diagramKind(input), model: model(src) }) + "\n");
}
