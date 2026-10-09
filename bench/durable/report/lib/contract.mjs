// Derives the deck's meta.contract record from the Durable conformance gate's own output, so the "same rows" claim
// is computed, not typed. Inputs: durable/contract/corpus.toml (the manifest at the pin) and the report.json that
// `contract all --strict` writes ({ <impl>: { built, lanes, results: [{ id, owner, family, status, ... }] } }).
import { createHash } from "node:crypto";
import { IDENTITY_ROWS, ResultsError } from "./results.mjs";

/** The [pin] table and the [[scenario]] blocks of corpus.toml (flat key = value lines, as gen-corpus.mjs writes them). */
export function parseCorpus(text) {
	const pin = {};
	const scenarios = [];
	let cur;
	for (const raw of text.split("\n")) {
		const line = raw.trim();
		if (line === "" || line.startsWith("#")) continue;
		if (line === "[pin]") {
			cur = pin;
			continue;
		}
		if (line === "[[scenario]]") {
			cur = {};
			scenarios.push(cur);
			continue;
		}
		const m = /^([A-Za-z_][\w-]*)\s*=\s*(.*)$/.exec(line);
		if (!m || !cur) continue;
		const v = m[2];
		cur[m[1]] = v.startsWith('"') ? JSON.parse(v) : v.startsWith("[") ? JSON.parse(v) : v;
	}
	if (!pin.commit) throw new ResultsError("corpus.toml: no [pin] commit");
	if (scenarios.length === 0) throw new ResultsError("corpus.toml: no scenarios");
	return { pin, scenarios };
}

/**
 * Checks every deck build against the gate report and returns meta.contract.
 * builds: { TinyGo: "wasm-tinygo", ... } (deck build -> gate implementation); modules: { TinyGo: <bytes>, ... }.
 * A build passes only when every `ready` row of the corpus ran and passed for it; skipped rows must be skipped.
 */
export function contractRecord({ corpus, report, builds, modules, core, reportUrl }) {
	const problems = [];
	const ready = corpus.scenarios.filter((s) => s.status === "ready");
	const skipped = new Set(corpus.scenarios.filter((s) => s.status === "skipped").map((s) => s.id));
	const matrixRows = new Set(corpus.scenarios.filter((s) => s.runner === "matrix").map((s) => s.id));
	// The memory bound (durable/contract/lib/memory.mjs) is a candidate rule, checked only after a row's trace matched the
	// reference; the gate also applies it to the reference's run against itself. Such a row still proves determinism.
	const referenceOverBound = [];
	const identityHeld = (impl, x) => impl === "pi" && x.status === "fail" && /^memory grows /.test(String(x.detail?.problem ?? "")) && Number.isInteger(x.detail?.commits);
	// A ready row the gate skips because the reference itself runs none of it here (an e2e test that needs credentials):
	// nobody can run it, the gate gives the reason, and every implementation, the reference included, must show the same.
	const refSkip = new Set((report.pi?.results ?? []).filter((x) => x.status === "skipped" && /^the reference /.test(String(x.detail ?? ""))).map((x) => x.id));
	const check = (impl) => {
		const r = report[impl];
		if (!r) return problems.push(`report has no ${impl} run`);
		if (typeof r.built === "string" && / failed /.test(r.built)) problems.push(`${impl}: ${r.built}`);
		const byId = new Map(r.results.map((x) => [x.id, x]));
		const missing = [];
		for (const s of ready) {
			const x = byId.get(s.id);
			if (!x) missing.push(s.id);
			else if (identityHeld(impl, x)) referenceOverBound.push(s.id);
			else if (x.status === "skipped" && refSkip.has(s.id)) continue;
			else if (x.status !== "pass") problems.push(`${impl} ${s.id}: ${x.status}`);
		}
		// A partial run (row ids on the command line) is not the full corpus; one line, after the failing rows.
		if (missing.length) notRun.push(`${impl}: ${missing.length} of ${ready.length} ready rows did not run (${missing.slice(0, 5).join(", ")}${missing.length > 5 ? ", ..." : ""})`);
		for (const x of r.results) if (x.status === "skipped" && !skipped.has(x.id) && !refSkip.has(x.id)) problems.push(`${impl} ${x.id}: skipped, but the corpus marks it ready`);
	};
	const notRun = [];
	check("pi"); // the reference against itself: the determinism control
	for (const impl of Object.values(builds)) check(impl);
	problems.push(...notRun);
	if (matrixRows.size === 0) problems.push("corpus has no crash-matrix rows");
	// crash_matrix "pass" claims every crash-matrix row ran and passed; a pending or skipped one is not proven.
	for (const s of corpus.scenarios) if (s.runner === "matrix" && s.status !== "ready") problems.push(`crash-matrix row ${s.id} is ${s.status}, not ready`);
	const readyIds = new Set(ready.map((s) => s.id));
	for (const rows of Object.values(IDENTITY_ROWS)) for (const id of Object.values(rows)) if (!readyIds.has(id)) problems.push(`corpus has no ready row ${id} (per-size row identity)`);
	if (!/^https:\/\//.test(reportUrl ?? "")) problems.push("report URL must be https");
	if (!/^[0-9a-f]{7,40}$/.test(core ?? "")) problems.push("core must be a commit hash");
	for (const b of Object.keys(builds)) if (!modules[b]) problems.push(`no module given for build ${b}`);
	if (problems.length) throw new ResultsError(`the CONTRACT gate is not green:\n  ${problems.slice(0, 20).join("\n  ")}${problems.length > 20 ? `\n  ... ${problems.length - 20} more` : ""}`);
	return {
		status: "pass",
		corpus: "full",
		crash_matrix: "pass",
		reference: corpus.pin.commit.slice(0, 8),
		core,
		builds: Object.keys(builds),
		artifacts: Object.fromEntries(Object.entries(modules).map(([b, bytes]) => [b, createHash("sha256").update(bytes).digest("hex")])),
		// Every ready row passed, so each history size of the deck's standard workload has its gate row.
		identity: IDENTITY_ROWS,
		report_url: reportUrl,
		rows: { ready: ready.length, skipped: skipped.size, crash_matrix: matrixRows.size, pending_owner: corpus.scenarios.filter((s) => s.status === "pending").map((s) => s.id), ...(referenceOverBound.length ? { reference_over_memory_bound: [...new Set(referenceOverBound)] } : {}), ...(refSkip.size ? { skipped_by_reference: [...refSkip].filter((id) => ready.some((s) => s.id === id)) } : {}) },
	};
}
