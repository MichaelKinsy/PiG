#!/usr/bin/env node
// Upstream oracle for the Mermaid markdown transformer (internal/codingagent
// mermaid_transform.go). Drives pi's own createMermaidMarkdownTransformer from
// the pinned coding-agent dist, so the Go port can be diffed byte-for-byte.
// Reads one JSON object per line from argv[2]:
//   {md, mode, messageType, isStreaming, availableWidth}
// With no theme the output is plain art. The themed mode adds
//   theme: {hyperlinks, palette: {color: getFgAnsi(color)}, fg: {color: [prefix, suffix]}}
// which becomes Pi's theme object ({getFgAnsi, fg}) and the terminal's
// hyperlink capability (pi-tui setCapabilities), so themedLines of the Go
// transformer is covered with Go's own theme palette. Prints {input, out}
// where out is the transformed markdown string.
import { readFileSync } from "node:fs";
import { importPiMermaid, importPiTui } from "./mermaid-oracle.mjs";

const { createMermaidMarkdownTransformer } = await importPiMermaid();
const piTui = await importPiTui();

const corpusPath = process.argv[2];
if (!corpusPath) { console.error("usage: mermaid-transform-pi.mjs <corpus>"); process.exit(2); }

for (const raw of readFileSync(corpusPath, "utf8").split("\n")) {
	if (raw === "" || raw.startsWith("#")) continue;
	const input = JSON.parse(raw);
	const options = { getMode: () => input.mode };
	if (input.theme) {
		const { hyperlinks, palette, fg } = input.theme;
		piTui.setCapabilities({ images: null, trueColor: true, hyperlinks });
		options.theme = {
			getFgAnsi: (color) => {
				if (!(color in palette)) throw new Error(`theme has no ${color}`);
				return palette[color];
			},
			fg: (color, text) => fg[color][0] + text + fg[color][1],
		};
	}
	const transformer = createMermaidMarkdownTransformer(options);
	const out = transformer(input.md, {
		messageType: input.messageType,
		isStreaming: input.isStreaming,
		availableWidth: input.availableWidth,
	});
	process.stdout.write(JSON.stringify({ input, out }) + "\n");
}
