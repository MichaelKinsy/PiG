// Oracle for system prompt preparation (src/harness/prompt.ts). Lines:
//   R <hex messages> <hex JSON of replaySections as [[key,value]...]>
//   P <hex JSON {head, entries, messages, desired: [[k,v]...], tools, timestamp}> <hex JSON drafts>
import { writeFileSync } from "node:fs";
import { PI } from "./pimain.mjs";
import { rng, hex } from "./rng.mjs";

const p = await import(`${PI}/packages/durable/src/harness/prompt.ts`);
const tr = await import(`${PI}/packages/ai/src/utils/transcript.ts`);
const r = rng(Number(process.argv[2] ?? 9));
const out = process.argv[3] ?? new URL("../testdata/prompt.hex", import.meta.url).pathname;
const J = (v) => JSON.stringify(v);
const lines = [];

const keys = ["cwd", "date", "2", "1", "tools", "a b", "toString", "x", "0", "z"];
const text = () => r.pick(["", "a", "<cwd>\n/x\n</cwd>", "é😀", "line\nbreak", "long ".repeat(3), "v2"]);
const schema = () => ({ type: "object", properties: Object.fromEntries(Array.from({ length: r.int(3) }, () => [r.pick(["p", "q", "r", "1"]), { type: r.pick(["string", "number"]) }])), required: r.chance(0.5) ? ["p"] : [] });
const tool = (name) => ({ name, description: r.pick(["d", "d2", ""]), parameters: schema(), ...(r.chance(0.2) ? { constrainedSampling: r.chance(0.5) } : {}) });
const toolNames = ["read", "bash", "edit", "write", "ls"];
function sectionsPatch() {
	const o = {};
	for (let i = r.int(4); i > 0; i--) o[r.pick(keys)] = r.chance(0.25) ? null : text();
	return o;
}
function systemMessage() {
	const m = { role: "system", content: r.chance(0.2) ? "base" : "" };
	if (r.chance(0.8)) m.sections = sectionsPatch();
	if (r.chance(0.4)) m.toolsRemoved = Array.from({ length: r.int(3) }, () => ({ name: r.pick(toolNames) }));
	if (r.chance(0.5)) m.toolsAdded = Array.from({ length: r.int(3) }, () => tool(r.pick(toolNames)));
	m.timestamp = 1000 + r.int(100);
	return m;
}
const sorted = (m) => m;
for (let i = 0; i < 400; i++) {
	const messages = [];
	const entries = [];
	let id = 1;
	for (let k = r.int(6); k > 0; k--) {
		if (r.chance(0.6)) { messages.push(systemMessage()); entries.push({ id: id++, kind: "pi.system" }); }
		else { messages.push({ role: "user", content: "hi", timestamp: 1 }); entries.push({ id: id++, kind: "pi.user" }); }
	}
	const head = r.chance(0.3) ? { id: r.int(id + 1) } : undefined;
	const desired = new Map();
	for (let k = r.int(5); k > 0; k--) desired.set(r.pick(keys), text());
	const tools = [];
	const names = new Set();
	for (let k = r.int(4); k > 0; k--) { const n = r.pick(toolNames); if (!names.has(n)) { names.add(n); tools.push(tool(n)); } }
	if (r.chance(0.6)) { // desired tools reuse shown declarations so retained, changed and reordered tools are common
		const shown = tr.getCurrentTools(JSON.parse(J(messages)));
		tools.length = 0;
		for (const s of shown) if (r.chance(0.8)) tools.push(r.chance(0.2) ? { ...s, description: "changed" } : s);
		if (r.chance(0.3)) tools.reverse();
		if (r.chance(0.4)) { const n = r.pick(toolNames); if (!tools.some((x) => x.name === n)) tools.push(tool(n)); }
	}
	const view = { head, entries: entries.map((e) => ({ id: e.id, ...e })), messages };
	const ts = 5000 + i;
	const drafts = p.planSystemEntries(JSON.parse(J(view)), new Map(desired), JSON.parse(J(tools)), ts);
	lines.push(`P ${hex(J({ head: head ?? null, entries, messages, desired: [...desired], tools, timestamp: ts }))} ${hex(J(drafts))}`);
	lines.push(`R ${hex(J(messages))} ${hex(J([...p.replaySections(JSON.parse(J(messages)))]))}`);
}
writeFileSync(out, lines.join("\n") + "\n");
console.log(lines.length, "lines");
