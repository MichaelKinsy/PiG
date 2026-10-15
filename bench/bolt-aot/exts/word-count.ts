// SPDX-FileCopyrightText: Copyright (c) 2026 OpenSec (Pi-Bolt)
// SPDX-License-Identifier: MIT
// Copied unchanged below this header from Pi-Bolt bolt-v0.7.4, examples/plugins/extensions/word-count.ts
// (https://github.com/opensec-git/Pi-Bolt, MIT): the /words hot-loop example PLUGINS.md measures.
/**
 * Example extension: a `word_count` tool and a `/words` command.
 *
 * It is an ordinary Pi extension: it works the same loaded at run time from ~/.pi/agent/extensions. Two habits make it start and
 * run fast when it is compiled in (docs/PLUGINS.md):
 * - Only types come from @earendil-works/pi-coding-agent (`import type`). A value import from it evaluates Pi's whole public API at
 *   startup; `defineTool()` only helps type inference, and a typed object literal does the same.
 * - The hot loop makes no calls but `charCodeAt`, which the compiler turns into a load.
 */
import { Type } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ToolDefinition } from "@earendil-works/pi-coding-agent";
import { readFile } from "node:fs/promises";

/** Words are runs of characters other than whitespace. A plain loop: compiled ahead of time, it runs as machine code. */
function countWords(text: string): number {
	let words = 0;
	let inWord = false;
	for (let i = 0; i < text.length; i++) {
		const c = text.charCodeAt(i);
		const space = c === 32 || (c >= 9 && c <= 13) || c === 160 || c === 0x2028 || c === 0x2029;
		if (!space && !inWord) words++;
		inWord = !space;
	}
	return words;
}

const parameters = Type.Object({
	path: Type.String({ description: "Path of the file to count" }),
});

interface Counts {
	words: number;
	lines: number;
	characters: number;
}

const wordCountTool: ToolDefinition<typeof parameters, Counts> = {
	name: "word_count",
	label: "Word count",
	description: "Counts the words, lines and characters of a text file.",
	parameters,
	async execute(_toolCallId, params) {
		const text = await readFile(params.path, "utf8");
		const result = { words: countWords(text), lines: text.split("\n").length, characters: text.length };
		return {
			content: [{ type: "text", text: `${params.path}: ${result.words} words, ${result.lines} lines, ${result.characters} characters` }],
			details: result,
		};
	},
};

export default function wordCount(pi: ExtensionAPI) {
	pi.registerTool(wordCountTool);
	pi.registerCommand("words", {
		description: "Count the words of a file",
		handler: async (args, ctx) => {
			const path = args.trim();
			if (!path) {
				ctx.ui.notify("Usage: /words <file>", "warning");
				return;
			}
			const text = await readFile(path, "utf8");
			const start = performance.now();
			const words = countWords(text);
			ctx.ui.notify(`${path}: ${words} words (${(performance.now() - start).toFixed(1)} ms)`, "info");
		},
	});
}
