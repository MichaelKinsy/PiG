// The interleaving of a row (CONTRACT section 5.8): the order in which the scenario's API calls (`>` the call is made, `!` it throws;
// lib/apitrace.mjs) and the store's commits (`#`) happen. A call's return is not a token: when a promise settles is visible only
// through what the scenario does next, which is the next `>`. Pi decides it by microtask scheduling; a candidate must reproduce it
// exactly. A commit token names the tables and ops it changed (the commit's content is the gate's commit level; here it only has
// to be recognisable) and every token keeps its microtask tick for the report.
export const IGNORED_TABLES = new Set(["durable_metadata", "record_ids"])

/** The tokens of one trace (an array of parsed trace lines), in file order. */
export function tokens(lines) {
	const out = []
	for (const l of lines) {
		if (l.t === "call") { if (l.phase !== "return") out.push({ k: l.phase === "enter" ? ">" : "!", name: l.name, tick: l.tick }) }
		else if (l.t === "commit" && l.seq !== undefined) {
			const summary = [...new Set((l.changes ?? []).filter(c => !IGNORED_TABLES.has(c.table)).map(c => `${c.table}:${c.op[0]}`))].sort().join(" ")
			out.push({ k: "#", seq: l.seq, summary, tick: l.tick, ...(l.begin === undefined ? {} : { begin: l.begin }) })
		}
	}
	return out
}

export const text = t => (t.k === "#" ? `# ${t.seq} ${t.summary}` : `${t.k} ${t.name}`)

/**
 * Compare a golden token list with a candidate's. Equal when every token is the same, in order. Otherwise the first difference
 * with its neighbourhood: `expected` and `got` are the next tokens on each side, `before` the agreed ones just before.
 */
export function compareInterleavings(golden, actual, context = 4) {
	const n = Math.max(golden.length, actual.length)
	for (let i = 0; i < n; i++) {
		const a = golden[i], b = actual[i]
		if (a && b && text(a) === text(b)) continue
		const show = list => list.slice(i, i + context + 1).map(t => `${text(t)}${t.tick === undefined ? "" : t.begin === undefined ? ` @${t.tick}` : ` @${t.begin}..${t.tick}`}`)
		return { index: i, before: golden.slice(Math.max(0, i - context), i).map(text), expected: show(golden), got: show(actual) }
	}
	return undefined
}

/**
 * The tokens of one vitest test out of a vitest trace (scenarios/vitest.trace-setup.mjs): the `test` marker whose name contains
 * `testName` up to the next marker. A storage `commit-end` is a commit token (`# n <write types>`, n counting the test's commits;
 * its start tick is kept for the report).
 */
export function testTokens(lines, testName) {
	const start = lines.findIndex(l => l.t === "test" && l.name?.includes(testName))
	if (start < 0) return undefined
	const out = []
	let commits = 0
	const starts = []
	for (let i = start + 1; i < lines.length && lines[i].t !== "test"; i++) {
		const l = lines[i]
		if (l.t === "call") { if (l.phase !== "return") out.push({ k: l.phase === "enter" ? ">" : "!", name: l.name, tick: l.tick }) }
		else if (l.t === "storage" && l.phase === "commit") starts.push(l.tick)
		else if (l.t === "storage" && l.phase === "commit-end") out.push({ k: "#", seq: ++commits, summary: l.summary, tick: l.tick, ...(starts.length ? { begin: starts.shift() } : {}) })
	}
	return out
}
