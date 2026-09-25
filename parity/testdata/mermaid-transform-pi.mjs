#!/usr/bin/env node
// Upstream oracle for the Mermaid markdown transformer (internal/codingagent
// mermaid_transform.go). Drives pi's own createMermaidMarkdownTransformer from
// the pinned coding-agent dist with no theme (so output is plain art), so the Go
// port can be diffed byte-for-byte. Reads one JSON object per line from argv[2]:
//   {md, mode, messageType, isStreaming, availableWidth}
// and prints {input, out} where out is the transformed markdown string.
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
const { createMermaidMarkdownTransformer } = await import(
	pathToFileURL(join(root, "dist/modes/interactive/components/mermaid.js")).href);

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
