// Drive the installed pi-ai estimate.js (estimateMessageTokens, estimateContextTokens, estimateTextTokens) over transcripts from stdin:
// [[{role, ts, text, blocks: [{type, text, name, args}], stop, usage: {input, output, cacheRead, cacheWrite, total}}]] -> [{tokens, usageTokens, trailingTokens, lastUsageIndex, perMessage: [number]}].
// A block is {type: "text" | "thinking" | "image" | "toolCall"}; args is JSON text.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { estimateContextTokens, estimateMessageTokens } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/utils/estimate.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const block = (b) => {
	if (b.type === "text") return { type: "text", text: b.text };
	if (b.type === "image") return { type: "image", data: "AAAA", mimeType: "image/png" };
	if (b.type === "thinking") return { type: "thinking", thinking: b.text };
	return { type: "toolCall", id: "c", name: b.name, arguments: JSON.parse(b.args) };
};
const usage = (u) => ({ input: u.input, output: u.output, cacheRead: u.cacheRead, cacheWrite: u.cacheWrite, totalTokens: u.total, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const message = (m) => {
	switch (m.role) {
		case "system": return { role: "system", content: m.text, timestamp: m.ts };
		case "user": return { role: "user", content: m.blocks === null ? m.text : m.blocks.map(block), timestamp: m.ts };
		case "toolResult": return { role: "toolResult", toolCallId: "c", toolName: "t", content: m.blocks.map(block), isError: false, timestamp: m.ts };
		default: return { role: "assistant", content: m.blocks.map(block), api: "x", provider: "p", model: "m", usage: usage(m.usage), stopReason: m.stop, timestamp: m.ts };
	}
};
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((messages) => {
			const converted = messages.map(message);
			return { ...estimateContextTokens(converted), perMessage: converted.map(estimateMessageTokens) };
		}),
	),
);
