// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
// Counts what each harness SENT: reads DIR/<session>/req-*.json (OpenAI chat-completions bodies logged by fake_model_ctx.py --dump) and prints
// per-session JSON: tokens of the system prompt, tool schemas and conversation per request (o200k_base and cl100k_base via gpt-tokenizer), and the
// prefix shared with the previous request (what implicit prompt caching can reuse). Usage: node tok.mjs DIR  (run where gpt-tokenizer resolves)
import { readdirSync, readFileSync, statSync } from "node:fs";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
const require = createRequire(process.env.TOK_RESOLVE_FROM ?? import.meta.url);
const enc = { o200k: require("gpt-tokenizer/encoding/o200k_base").encode, cl100k: require("gpt-tokenizer/encoding/cl100k_base").encode };
const text = (c) => (typeof c === "string" ? c : Array.isArray(c) ? c.map((p) => (p.type === "text" ? p.text : JSON.stringify(p))).join("\n") : JSON.stringify(c ?? ""));
const tok = (s) => ({ o200k: enc.o200k(s).length, cl100k: enc.cl100k(s).length });
const add = (a, b) => ({ o200k: a.o200k + b.o200k, cl100k: a.cl100k + b.cl100k });
const zero = () => ({ o200k: 0, cl100k: 0 });
// Cache markers are fields of the request, not words inside the conversation text (the scripted file reads contain such words).
const MARK = new Set(["cache_control", "prompt_cache_key", "prompt_cache_retention", "cache_retention"]);
function countMarkers(v) {
	if (Array.isArray(v)) return v.reduce((n, x) => n + countMarkers(x), 0);
	if (v && typeof v === "object") return Object.entries(v).reduce((n, [k, x]) => n + (MARK.has(k) ? 1 : 0) + countMarkers(x), 0);
	return 0;
}
const out = {};
for (const session of readdirSync(process.argv[2]).sort()) {
	const dir = `${process.argv[2]}/${session}`;
	if (!statSync(dir).isDirectory()) continue;
	const reqs = readdirSync(dir).filter((f) => f.startsWith("req-")).sort();
	let prev = null;
	const rows = [];
	for (const f of reqs) {
		const body = JSON.parse(readFileSync(`${dir}/${f}`, "utf8"));
		const raw = readFileSync(`${dir}/${f}`, "utf8");
		const elems = []; // [kind, text, tokens] in prompt order: tools, system, messages
		const tools = JSON.stringify(body.tools ?? []);
		elems.push(["tools", tools, tok(tools)]);
		let sys = zero(), msgs = zero();
		for (const m of body.messages ?? []) {
			const t = text(m.content) + (m.tool_calls ? JSON.stringify(m.tool_calls) : "");
			const k = tok(t);
			const withOverhead = { o200k: k.o200k + 4, cl100k: k.cl100k + 4 };
			const kind = m.role === "system" || m.role === "developer" ? "system" : "message";
			elems.push([kind, `${m.role}:${t}`, withOverhead]);
			if (kind === "system") sys = add(sys, withOverhead);
			else msgs = add(msgs, withOverhead);
		}
		const hashes = elems.map((e) => createHash("sha1").update(e[0] + e[1]).digest("hex"));
		let shared = zero();
		if (prev) {
			for (let i = 0; i < Math.min(hashes.length, prev.length) && hashes[i] === prev[i]; i++) shared = add(shared, elems[i][2]);
		}
		prev = hashes;
		const toolsTok = elems[0][2];
		const total = add(add(sys, toolsTok), msgs);
		rows.push({ file: f, bytes: raw.length, bodyKeys: Object.keys(body), tools: (body.tools ?? []).length, cacheMarkers: countMarkers(body),
			system: sys, toolSchemas: toolsTok, conversation: msgs, total, sharedPrefix: shared, messages: (body.messages ?? []).length });
	}
	out[session] = rows;
}
console.log(JSON.stringify(out));
