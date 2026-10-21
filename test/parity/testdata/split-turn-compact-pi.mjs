// Pi's compact() (core/compaction/compaction.ts) on a split turn: the requests it sends and the summary it returns, with and without /compact instructions (Pi adfc1446, #11058).
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent") + "/";
const { compact } = await import(pathToFileURL(root + "dist/core/compaction/compaction.js"));
const ai = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/index.js"));
const model = { id: "test-model", name: "Test Model", api: "anthropic-messages", provider: "anthropic", baseUrl: "https://api.anthropic.com", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 8192 };
const user = (text) => ({ role: "user", content: text, timestamp: 1 });
const assistant = (text) => ({ role: "assistant", content: [{ type: "text", text }], api: "anthropic-messages", provider: "anthropic", model: "test-model", usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: 2 });
const prefix = [user("build the parser"), assistant("started on lexer.go")];
const history = [user("earlier request"), assistant("earlier answer")];
const cases = [
	{ name: "prefix-only-instructions", history: [], previous: "previous checkpoint", instructions: "keep migration decisions" },
	{ name: "prefix-only-no-instructions", history: [], previous: "previous checkpoint", instructions: undefined },
	{ name: "history-and-prefix-instructions", history, previous: undefined, instructions: "keep migration decisions" },
	{ name: "history-and-prefix-previous-instructions", history, previous: "previous checkpoint", instructions: "focus on lexer.go" },
];
for (const tc of cases) {
	const prompts = [];
	const streamFn = (m, context) => {
		prompts.push(context.messages.filter((x) => x.role === "user").flatMap((x) => (typeof x.content === "string" ? [x.content] : x.content.map((c) => c.text))).join(""));
		const stream = ai.createAssistantMessageEventStream();
		const message = { ...assistant(`summary ${prompts.length}`), usage: { input: 7, output: 3, cacheRead: 0, cacheWrite: 0, totalTokens: 10, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
		stream.push({ type: "done", reason: "stop", message });
		stream.end(message);
		return stream;
	};
	const preparation = { firstKeptEntryId: "keep", messagesToSummarize: tc.history, turnPrefixMessages: prefix, isSplitTurn: true, tokensBefore: 100, previousSummary: tc.previous, fileOps: { read: new Set(), written: new Set(), edited: new Set() }, settings: { enabled: true, reserveTokens: 2000, keepRecentTokens: 20 } };
	const result = await compact(preparation, model, "test-key", undefined, tc.instructions, undefined, undefined, streamFn);
	console.log(`SPLIT_TURN ${tc.name} prompts=${JSON.stringify(prompts)} summary=${JSON.stringify(result.summary)}`);
}
