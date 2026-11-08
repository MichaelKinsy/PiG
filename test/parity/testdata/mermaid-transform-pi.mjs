#!/usr/bin/env node
// Upstream oracle for the Mermaid markdown transformer (internal/codingagent
// mermaid_transform.go). Drives pi's own createMermaidMarkdownTransformer from
// the pinned coding-agent dist with no theme (so output is plain art), so the Go
// port can be diffed byte-for-byte. Reads one JSON object per line from argv[2]:
//   {md, mode, messageType, isStreaming, availableWidth}
// and prints {input, out} where out is the transformed markdown string.
import { readFileSync } from "node:fs";
import { importPiMermaid } from "./mermaid-oracle.mjs";

const { createMermaidMarkdownTransformer } = await importPiMermaid();

const corpusPath = process.argv[2];
if (!corpusPath) { console.error("usage: mermaid-transform-pi.mjs <corpus>"); process.exit(2); }

for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw);
	const transformer = createMermaidMarkdownTransformer({ getMode: () => input.mode });
	const out = transformer(input.md, {
		messageType: input.messageType,
		isStreaming: input.isStreaming,
		availableWidth: input.availableWidth,
	});
	process.stdout.write(JSON.stringify({ input, out }) + "\n");
}
