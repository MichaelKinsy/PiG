// Parses, validates and aggregates the one results file the deck is rendered from.
// Format and gates: ../README.md. Metric definitions are the ones durable-bench's published slides state (README.md).
import { createHash } from "node:crypto";

export const SIZES = [50, 250, 1000, 3500];
export const STATUSES = ["placeholder", "draft", "final"];
export const KINDS = ["pi", "tardigrade", "pig", "control", "floor"];
// detail: another build of the gated PiG core. control: a design control (the vendored TS core of the gate), shown
// only on the builds slide under its own label, never as PiG Durable.
// floor: the empty target (durable-bench src/empty.ts), the request path with no agent; latencies are reported net of it.
export const ROLES = ["headline", "reference", "contender", "detail", "control", "floor"];
// Every number is tagged with the rule it was measured under. Rule A: durable-bench's JS model on the same path as
// pi-durable (a Durable Object on Miniflare or Cloudflare); the headline numbers. Rule B: the native build, which runs
// its own scripted model in the core's harness; a harness floor, never compared with Rule A as a contest.
export const RULES = {
	A: "Rule A: durable-bench's JS model in a Durable Object, the same model path as pi-durable (the headline)",
	B: "Rule B: the native build's own scripted model in the core's harness (a harness floor)",
};
// Final decks report p50, p95 and max over at least this many warm turns per target and size (pooled over samples).
export const MIN_WARM_TURNS = 100;
// native: the core built as a native process (not a Durable Object); only on the builds slide.
export const HOSTS = ["miniflare", "cloudflare", "native"];
export const SEED_PROTOCOLS = ["scratch", "chain"];
// Where-it-runs probe (RUNNER-SPEC 4a): "count" runs measure everything but the time inside the core; "time" runs add it.
export const PROBE_MODES = ["count", "time"];
export const PROBE_FIELDS = ["ms", "cpuMs", "crossings", "hopSteps", "coreMs", "instantiateMs", "rowsRead", "rowsWritten", "wasmBytes", "heapUsed", "backing"];
// On Cloudflare (bench/cloud.ts): CPU time from the invocation logs; no heap (not observable) and no timers inside a request.
export const CF_PROBE_FIELDS = ["ms", "cpuMs", "crossings", "hopSteps", "rowsRead", "rowsWritten", "wasmBytes"];
export const CF_UNOBSERVABLE = ["heapUsed", "heapTotal", "backing"];
// Cloudflare limits (developers.cloudflare.com/workers/platform/limits, read 2026-10-08).
export const LIMITS = { memoryBytes: 128e6, uploadBytes: 64 * 1024 * 1024, cpuFreeMs: 10, cpuPaidMs: 30_000 };
// The gate's bench rows whose histories are the deck's: W1-<n> runs durable-bench's standard scenario on the
// pi-durable-main-written fixture with the published fingerprint (durable/contract/corpus.toml, fixtures cache).
export const IDENTITY_ROWS = { standard: Object.fromEntries([50, 250, 1000, 3500].map((n) => [n, `W1-${n}`])) };

// durable-bench's context fingerprint of the seeded history (8 bytes of sha256 in plan.ts form), the same for
// pi-durable 1.0.4, pi-durable main and tardigrade fixtures (durable-bench-pig fixtures README, repro step 1).
export const SEED_FINGERPRINTS = {
	standard: { 50: "b017b487524e44a4", 250: "dcea9f30b0917245", 1000: "ac520308146f2a8f", 3500: "0a8c8e4b0d9a0794" },
	// Realistic variants written by pi-durable main (durable-bench-pig b3af4572e, bench/durable/fixtures/README.md).
	// Changing a variant's agent changes its fingerprints: update these together with the variant.
	compact: { 50: "b017b487524e44a4", 250: "dcea9f30b0917245", 1000: "0d02b0801c0f29cf", 3500: "e34c4c0615e83ae4" },
	big: { 50: "d46fda2bd29cb113", 250: "60709a6a093c8d8d", 1000: "aada075064a4b5d0", 3500: "68e9ddb1d0c051bb" },
	"compact-big": { 50: "d46fda2bd29cb113", 250: "c94e2cd5e486ac7a", 1000: "48d124e181b57374", 3500: "85d1ef2ab8e386a4" },
};

// Benchmark-slice prototype cores. Owner rule: no public numbers from them, in any status.
// wasm-spike 872c81bf5 (durable-repro step 2 pigwasm targets), durable-perf 407c624b7 (durable-bench-pig native).
export const PROTOTYPE_CORES = ["872c81bf5", "407c624b7"];

export class ResultsError extends Error {}
const fail = (msg) => {
	throw new ResultsError(msg);
};
const need = (cond, msg) => cond || fail(msg);

/** A seed run that built its history from an empty store. Others (`from` "250", "from250", ...) are increments. */
export const fromScratch = (s) => s.from === undefined || s.from === "empty";
/** The history size a seed run started from: 0 for an empty store. */
export const fromTurns = (s) => (fromScratch(s) ? 0 : Number(String(s.from).replace(/^from/, "")));

export function sha256(text) {
	return createHash("sha256").update(text).digest("hex");
}

/** Splits a results file into its meta line, samples and seeds. Throws on malformed or unknown lines. */
export function parse(text) {
	const lines = text.split("\n").filter((l) => l.trim() !== "");
	need(lines.length > 0, "results file is empty");
	const out = { meta: undefined, samples: [], seeds: [], probes: [], sizes: [], sha256: sha256(text) };
	lines.forEach((line, i) => {
		let d;
		try {
			d = JSON.parse(line);
		} catch (e) {
			fail(`line ${i + 1}: not JSON (${e.message})`);
		}
		const t = d.t ?? "sample";
		if (t === "meta") {
			need(i === 0, `line ${i + 1}: the meta line must be the first line`);
			out.meta = d;
		} else if (t === "sample") out.samples.push({ ...d, workload: d.workload ?? "standard", host: d.host ?? "miniflare", line: i + 1 });
		else if (t === "seed") out.seeds.push({ ...d, workload: d.workload ?? "standard", line: i + 1 });
		else if (t === "probe") out.probes.push({ ...d, host: d.host ?? "miniflare", line: i + 1 });
		else if (t === "size") out.sizes.push({ ...d, line: i + 1 });
		else fail(`line ${i + 1}: unknown line type ${JSON.stringify(t)}`);
	});
	need(out.meta, "line 1: missing meta line");
	return out;
}

const num = (v) => typeof v === "number" && Number.isFinite(v) && v >= 0;
const isPrototype = (version) => PROTOTYPE_CORES.some((p) => String(version).startsWith(p) || (String(version).length >= 7 && p.startsWith(String(version))));

/** Returns the target keys by role. */
export function roles(meta) {
	const by = (role) => Object.keys(meta.targets).filter((k) => meta.targets[k].role === role);
	return { headline: by("headline")[0], reference: by("reference")[0], contender: by("contender")[0], detail: by("detail"), control: by("control"), floor: by("floor")[0] };
}

/**
 * Checks shape, consistency and the honesty gates. Returns a list of warnings; throws ResultsError on a violation.
 * Gates: placeholder files are wholly synthetic; real PiG numbers require a green CONTRACT gate on that exact core;
 * prototype cores never render; final files are complete and like-for-like.
 */
export function validate(data) {
	const { meta, samples, seeds, probes = [], sizes = [] } = data;
	const warnings = [];
	need(STATUSES.includes(meta.status), `meta.status must be one of ${STATUSES.join(", ")}`);
	const placeholder = meta.status === "placeholder";

	// Targets and roles.
	need(meta.targets && typeof meta.targets === "object", "meta.targets missing");
	for (const [k, t] of Object.entries(meta.targets)) {
		need(typeof t.label === "string" && t.label, `target ${k}: label missing`);
		need(KINDS.includes(t.kind), `target ${k}: kind must be one of ${KINDS.join(", ")}`);
		need(ROLES.includes(t.role), `target ${k}: role must be one of ${ROLES.join(", ")}`);
		need(typeof t.version === "string" && t.version, `target ${k}: version missing`);
		if (t.kind === "pig" || t.kind === "control") need(typeof t.build === "string" && t.build, `target ${k}: PiG and control targets name their build (TinyGo, Go, native, TS)`);
		need((t.kind === "control") === (t.role === "control"), `target ${k}: a design control has kind and role "control", and nothing else does`);
		need((t.kind === "floor") === (t.role === "floor"), `target ${k}: the empty-target floor has kind and role "floor", and nothing else does`);
		// durable-bench cannot run a native process; the native host runs its own port of the scripted model, and says so.
		if (t.build === "native" && meta.status !== "placeholder") need(typeof t.model === "string" && t.model, `target ${k}: a native build names the model it ran (target.model, RUNNER-SPEC "Native build")`);
	}
	const r = roles(meta);
	const count = (role) => Object.values(meta.targets).filter((t) => t.role === role).length;
	need(count("headline") === 1 && meta.targets[r.headline].kind === "pig", "exactly one headline target, of kind pig");
	need(count("reference") === 1 && meta.targets[r.reference].kind === "pi", "exactly one reference target, of kind pi (pi-durable main)");
	need(count("contender") === 1 && meta.targets[r.contender].kind === "tardigrade", "exactly one contender target, of kind tardigrade");
	for (const k of r.detail) need(meta.targets[k].kind === "pig", `detail target ${k} must be a PiG build`);
	need(r.control.length <= 1, "at most one design control target");
	need(count("floor") <= 1, "at most one floor target");

	// Workloads.
	need(meta.workloads && meta.workloads.standard, "meta.workloads.standard missing");
	for (const [k, w] of Object.entries(meta.workloads)) {
		need(typeof w.title === "string" && typeof w.description === "string", `workload ${k}: title and description required`);
		const count = (v) => Number.isInteger(v) && v >= 0;
		for (const [n, f] of Object.entries(w.fixtures ?? {}))
			need(
				/^[1-9]\d*$/.test(n) && count(f?.entries) && count(f?.entry_bytes) && [f?.compactions, f?.active_entries].every((v) => v === undefined || count(v)),
				`workload ${k}: fixtures.${n} needs whole-number entries and entry_bytes (compactions, active_entries optional)`,
			);
	}

	// Lines.
	for (const s of samples) {
		const at = `line ${s.line}`;
		need(meta.targets[s.target], `${at}: unknown target ${s.target}`);
		need(s.version === meta.targets[s.target].version, `${at}: version ${s.version} is not target ${s.target}'s ${meta.targets[s.target].version}`);
		need(meta.workloads[s.workload], `${at}: unknown workload ${s.workload}`);
		need(HOSTS.includes(s.host), `${at}: host must be one of ${HOSTS.join(", ")}`);
		need((s.host === "native") === (meta.targets[s.target].build === "native"), `${at}: only the native build runs on host native, and only there`);
		if (meta.targets[s.target].kind === "floor") need(s.host === "miniflare" && s.workload === "standard", `${at}: the floor runs on Miniflare, standard workload`);
		need(Number.isInteger(s.turns) && s.turns > 0, `${at}: turns`);
		need(num(s.open) && Array.isArray(s.turn) && s.turn.length >= 2 && s.turn.every(num), `${at}: open and turn[] (>= 2 turns) required`);
		need(num(s.bytes), `${at}: bytes required`);
		need(!!s.placeholder === placeholder, `${at}: ${placeholder ? "placeholder files hold only placeholder lines" : "placeholder line in a measured results file"}`);
	}
	for (const s of seeds) {
		const at = `line ${s.line}`;
		need(meta.targets[s.target], `${at}: unknown seed target ${s.target}`);
		need(s.version === meta.targets[s.target].version, `${at}: seed version ${s.version} is not target ${s.target}'s ${meta.targets[s.target].version}`);
		need(meta.workloads[s.workload], `${at}: unknown workload ${s.workload}`);
		need(num(s.seedMs) && Number.isInteger(s.turns), `${at}: seedMs and turns required`);
		need(s.from === undefined || (typeof s.from === "string" && Number.isInteger(fromTurns(s)) && fromTurns(s) < s.turns), `${at}: from must name the size the run started from ("empty", "250", "from250")`);
		// Runs the slides use must prove their history; increments are kept for the tables and checked when they carry one.
		need(typeof s.fingerprint === "string" || (!fromScratch(s) && s.fingerprint === undefined), `${at}: fingerprint required for a from-scratch seed run`);
		need(!!s.placeholder === placeholder, `${at}: ${placeholder ? "placeholder files hold only placeholder lines" : "placeholder line in a measured results file"}`);
	}

	// Where-it-runs probe lines (durable-bench bench/probe.ts, RUNNER-SPEC 4a) and upload sizes (bench/size.ts): only
	// targets that run in a Durable Object, i.e. pi-durable main and the Wasm builds of PiG.
	const inDO = (k) => meta.targets[k] && (meta.targets[k].kind === "pi" || (meta.targets[k].kind === "pig" && meta.targets[k].build !== "native"));
	for (const p of probes) {
		const at = `line ${p.line}`;
		need(inDO(p.target), `${at}: probe target ${p.target} must be pi-durable main or a Wasm build of PiG`);
		need(p.version === meta.targets[p.target].version, `${at}: probe version ${p.version} is not target ${p.target}'s ${meta.targets[p.target].version}`);
		need(PROBE_MODES.includes(p.mode), `${at}: probe mode must be one of ${PROBE_MODES.join(", ")}`);
		need(p.host === "miniflare" || p.host === "cloudflare", `${at}: probe host must be miniflare or cloudflare`);
		need(Number.isInteger(p.turns) && p.turns > 0, `${at}: turns`);
		const cfp = p.host === "cloudflare";
		const fields = cfp ? CF_PROBE_FIELDS : PROBE_FIELDS;
		const req = (x) => x && typeof x === "object" && fields.every((f) => num(x[f])) && (!cfp || CF_UNOBSERVABLE.every((f) => x[f] === null));
		need(req(p.open) && Array.isArray(p.turn) && p.turn.length >= 2 && p.turn.every(req), `${at}: open and turn[] (>= 2) each need ${fields.join(", ")}${cfp ? `, with ${CF_UNOBSERVABLE.join(", ")} null (not observable on Cloudflare)` : ""}`);
		if (cfp) need(p.mode === "count", `${at}: Cloudflare freezes timers during a request; only count probes run there`);
		// Retained memory: one reading after a full collection, after the last turn (Miniflare only).
		if (p.retained !== undefined) need(!cfp && ["heapUsed", "backing", "wasmBytes"].every((f) => num(p.retained[f])), `${at}: retained needs heapUsed, backing and wasmBytes (Miniflare only)`);
		need(!!p.placeholder === placeholder, `${at}: ${placeholder ? "placeholder files hold only placeholder lines" : "placeholder line in a measured results file"}`);
	}
	for (const z of sizes) {
		const at = `line ${z.line}`;
		need(inDO(z.target), `${at}: size target ${z.target} must be pi-durable main or a Wasm build of PiG`);
		need(z.version === meta.targets[z.target].version, `${at}: size version ${z.version} is not target ${z.target}'s ${meta.targets[z.target].version}`);
		need(num(z.bytes) && num(z.gzip) && num(z.wasm ?? 0), `${at}: bytes and gzip required`);
		need(!!z.placeholder === placeholder, `${at}: ${placeholder ? "placeholder files hold only placeholder lines" : "placeholder line in a measured results file"}`);
	}

	// Like-for-like: every target built the same history. Standard histories equal durable-bench's published fingerprints;
	// other workloads must agree across targets.
	const byWorkloadSize = new Map();
	for (const s of seeds.filter((x) => x.fingerprint !== undefined)) {
		const known = SEED_FINGERPRINTS[s.workload]?.[s.turns];
		if (known) need(s.fingerprint === known, `line ${s.line}: ${s.target} ${s.workload} ${s.turns} fingerprint ${s.fingerprint} is not durable-bench's ${known}`);
		const k = `${s.workload}/${s.turns}`;
		const prev = byWorkloadSize.get(k);
		if (prev) need(prev.fingerprint === s.fingerprint, `line ${s.line}: ${s.target} ${k} fingerprint ${s.fingerprint} differs from ${prev.target}'s ${prev.fingerprint}`);
		else byWorkloadSize.set(k, s);
	}

	// PiG gate. Applies to every non-placeholder file that holds a PiG line.
	const pigTargets = Object.keys(meta.targets).filter((k) => meta.targets[k].kind === "pig");
	for (const k of [...pigTargets, ...r.control]) need(!isPrototype(meta.targets[k].version), `target ${k}: ${meta.targets[k].version} is a benchmark-slice prototype core; owner rule: no numbers from it`);
	const pigLines = [...samples, ...seeds, ...probes, ...sizes].filter((s) => meta.targets[s.target].kind === "pig");
	if (!placeholder && pigLines.length > 0) {
		const c = meta.contract ?? fail("PiG lines present but meta.contract is missing: render real PiG numbers only after the CONTRACT gate is green");
		need(c.status === "pass", `meta.contract.status is ${JSON.stringify(c.status)}: PiG numbers render only from a production core that passed the CONTRACT gate`);
		need(c.corpus === "full", "meta.contract.corpus must be \"full\" (row identity on the full corpus)");
		need(c.crash_matrix === "pass", "meta.contract.crash_matrix must be \"pass\"");
		need(c.reference === meta.targets[r.reference].version, `meta.contract.reference ${c.reference} is not the reference target's version ${meta.targets[r.reference].version}`);
		need(typeof c.core === "string" && c.core.length >= 7, "meta.contract.core: the gated core commit");
		need(/^https:\/\//.test(c.report_url ?? ""), "meta.contract.report_url: an https link to the row-identity verification");
		need(Array.isArray(c.builds), "meta.contract.builds: the builds the CONTRACT gate ran (each build is gated, not only the core source)");
		// The slides state how many corpus rows passed and how many the gate skips; contract-record.mjs writes both.
		need(Number.isInteger(c.rows?.ready) && c.rows.ready > 0 && Number.isInteger(c.rows?.skipped) && c.rows.skipped >= 0, "meta.contract.rows: { ready, skipped } corpus row counts (contract-record.mjs)");
		for (const k of pigTargets) {
			need(meta.targets[k].version === c.core, `target ${k}: version ${meta.targets[k].version} is not the gated core ${c.core}`);
			need(c.builds.includes(meta.targets[k].build), `target ${k}: build ${meta.targets[k].build} did not pass the CONTRACT gate (meta.contract.builds)`);
			// The measured module is the gated module, byte for byte.
			const gated = c.artifacts?.[meta.targets[k].build];
			need(/^[0-9a-f]{64}$/.test(gated ?? ""), `meta.contract.artifacts.${meta.targets[k].build}: sha256 of the module the CONTRACT gate ran`);
			need(meta.targets[k].artifact?.sha256 === gated, `target ${k}: measured module sha256 ${meta.targets[k].artifact?.sha256} is not the gated ${gated}`);
		}
		// Per-cell row identity: each history size of a workload the gate covers names the passing gate row.
		for (const [w, rows] of Object.entries(c.identity ?? {})) {
			need(IDENTITY_ROWS[w], `meta.contract.identity.${w}: the gate has no bench row for this workload's histories`);
			for (const [n, row] of Object.entries(rows)) need(IDENTITY_ROWS[w][n] === row, `meta.contract.identity.${w}.${n}: ${row} is not the gate row for that history (${IDENTITY_ROWS[w][n]})`);
		}
	}

	if (meta.status === "final") {
		need(!JSON.stringify(meta).includes("TODO"), "final: meta still holds a TODO from the template");
		const m = meta.machine ?? {};
		for (const f of ["host", "cpu", "pinning", "relative"]) need(typeof m[f] === "string" && m[f], `final: meta.machine.${f} required (footer honesty)`);
		need(typeof meta.model?.label === "string", "final: meta.model.label required (like-for-like model)");
		need(meta.session?.interleaved === true, "final: meta.session.interleaved must be true (one interleaved session)");
		// durable-bench's seed.ts reports the time since the previous size of one invocation: a 50/250/1000/3500 ladder
		// would report 1,000 -> 3,500 as "3,500". The final session seeds every size from zero in its own invocation.
		// "chain": contiguous fingerprinted segments 0 -> 50 -> ... -> n of one pass add up to the time from zero.
		need(SEED_PROTOCOLS.includes(meta.protocol?.seed), `final: meta.protocol.seed must be one of ${SEED_PROTOCOLS.join(", ")} (RUNNER-SPEC.md)`);
		if (pigLines.length > 0) for (const n of SIZES) need(meta.contract.identity?.standard?.[n], `final: meta.contract.identity.standard.${n}: the gate row proving row identity at that size`);
		need(typeof meta.session?.id === "string", "final: meta.session.id required");
		const min = meta.protocol?.samples ?? fail("final: meta.protocol.samples required");
		need(r.floor, "final: a floor target (durable-bench's empty target): latencies are reported net of it");
		const warmTurns = (k, n, host = "miniflare") => samples.filter((s) => s.target === k && s.workload === "standard" && s.host === host && s.turns === n).reduce((a, s) => a + s.turn.length - 1, 0);
		for (const k of [r.headline, r.reference, r.contender, r.floor]) {
			for (const n of SIZES) {
				const got = samples.filter((s) => s.target === k && s.workload === "standard" && s.host === "miniflare" && s.turns === n).length;
				need(got >= min, `final: ${k} standard ${n}: ${got} samples, protocol needs ${min}`);
				need(warmTurns(k, n) >= MIN_WARM_TURNS, `final: ${k} standard ${n}: ${warmTurns(k, n)} warm turns, p50/p95/max need ${MIN_WARM_TURNS}`);
				if (k !== r.floor) need(seedTimes(seeds, k, n, "standard").length > 0, `final: ${k} standard ${n}: no from-scratch seed run or complete seed chain`);
			}
		}
		// The owner's where-it-runs question (RUNNER-SPEC 4a) is answered in every final deck with PiG numbers.
		if (pigLines.length > 0) {
			// Rows written and the Wasm memory high-water mark are reported at every size.
			for (const k of [r.headline, r.reference])
				for (const n of SIZES) need(probes.some((p) => p.target === k && p.turns === n && p.mode === "count" && p.host === "miniflare"), `final: ${k} ${n}: no where-it-runs probe sample (BENCH_PROBE=count)`);
			// The memory row compares what the isolate retains; a reading before collection counts garbage.
			for (const p of probes) if (p.mode === "count" && p.host === "miniflare" && (p.target === r.headline || p.target === r.reference)) need(p.retained !== undefined, `final: line ${p.line}: count probe without the retained-memory reading (where-it-runs.patch takes it after the last turn)`);
			for (const n of [50, 3500]) need(probes.some((p) => p.target === r.headline && p.turns === n && p.mode === "time" && p.host === "miniflare"), `final: ${r.headline} ${n}: no where-it-runs probe sample (BENCH_PROBE=time)`);
			for (const k of [r.headline, r.reference]) need(sizes.some((z) => z.target === k), `final: ${k}: no upload size line (bench/size.ts)`);
		}
		const cf = [...samples, ...probes].some((s) => s.host === "cloudflare");
		if (cf) need(meta.cloudflare?.status === "measured" && typeof meta.cloudflare.label === "string", "final: Cloudflare samples need meta.cloudflare {status: measured, label}");
	}
	if (meta.status !== "placeholder" && [...samples, ...probes].some((s) => s.host === "cloudflare") && meta.cloudflare?.status !== "measured") fail("Cloudflare samples need meta.cloudflare.status \"measured\"");
	if (!placeholder && pigLines.length === 0) warnings.push("no PiG lines: the PiG series render as dashes");
	return warnings;
}

/**
 * Times (ms) to build the history of `n` turns from an empty store. From-scratch runs to n count as they are. When
 * there are none, each pass (`pass`, else `sample`) whose segments chain 0 -> ... -> n without a gap adds the sum of
 * its segments: durable-bench's seed.ts restarts its timer at every size of one invocation, and each segment's end
 * state is proved by its fingerprint, so the next segment starts from that same history. Segments without a
 * fingerprint, or passes with a gap, add nothing; two segments of one pass from the same size are an error.
 */
export function seedTimes(seeds, target, n, workload = "standard") {
	const mine = seeds.filter((x) => x.target === target && x.workload === workload);
	const direct = mine.filter((x) => x.turns === n && fromScratch(x)).map((x) => x.seedMs);
	if (direct.length > 0) return direct;
	const passes = new Map();
	for (const x of mine.filter((x) => typeof x.fingerprint === "string" && x.turns <= n)) {
		const key = x.pass ?? x.sample ?? 0;
		const byFrom = passes.get(key) ?? new Map();
		passes.set(key, byFrom);
		const from = fromTurns(x);
		if (byFrom.has(from)) fail(`seed chain ${target} ${workload} pass ${key}: two segments start at ${from} turns (lines ${byFrom.get(from).line}, ${x.line})`);
		byFrom.set(from, x);
	}
	const out = [];
	for (const byFrom of passes.values()) {
		let at = 0;
		let total = 0;
		while (at < n && byFrom.has(at)) {
			const x = byFrom.get(at);
			total += x.seedMs;
			at = x.turns;
		}
		if (at === n) out.push(total);
	}
	return out;
}

export const median = (values) => {
	if (values.length === 0) return undefined;
	const v = [...values].sort((a, b) => a - b);
	const mid = v.length >> 1;
	return v.length % 2 ? v[mid] : (v[mid - 1] + v[mid]) / 2;
};

export const quantile = (values, p) => {
	if (values.length === 0) return undefined;
	const v = [...values].sort((a, b) => a - b);
	const k = (v.length - 1) * p;
	const f = Math.floor(k);
	const c = Math.min(f + 1, v.length - 1);
	return v[f] + (v[c] - v[f]) * (k - f);
};

// Deterministic bootstrap of the median (seeded, so tables are reproducible byte for byte).
export function bootstrapMedian(values, rounds = 2000, seed = 1) {
	if (values.length < 2) return [undefined, undefined];
	let x = seed >>> 0;
	const rand = () => {
		x = (x + 0x6d2b79f5) >>> 0;
		let t = x;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
	const meds = [];
	for (let i = 0; i < rounds; i++) meds.push(median(values.map(() => values[Math.floor(rand() * values.length)])));
	return [quantile(meds, 0.025), quantile(meds, 0.975)];
}

/** Per-sample metrics. */
export const perSample = {
	cold: (s) => s.open + s.turn[0], // open + first turn, runtime startup excluded
	mb: (s) => s.bytes / 1e6, // SQLite bytes after the measured turns (sidecar tables included), decimal MB
	rss: (s) => s.rss,
};

/** The rule a target's numbers were measured under (RULES). */
export const ruleOf = (t) => (t.build === "native" ? "B" : "A");

// Latency observations of one cell. warm: every turn after the first, of every sample, pooled. cold: open + first turn,
// one per sample (a fresh runtime each).
export const LATENCY = {
	warm: (rows) => rows.flatMap((s) => s.turn.slice(1)),
	cold: (rows) => rows.map(perSample.cold),
};

export function makeQuery(data) {
	const { samples, seeds } = data;
	const rows = (target, n, { workload = "standard", host = "miniflare" } = {}) =>
		samples.filter((s) => s.target === target && s.turns === n && s.workload === workload && s.host === host);
	const floorKey = roles(data.meta).floor;
	// The floor a latency is net of: the p50 of the empty target's same latency at the same size, on Miniflare. The
	// native build (Rule B) has no request path, so none; Cloudflare samples carry the floor their run measured.
	const floor = (kind, target, n, host) => {
		if (host === "native" || target === floorKey) return 0;
		if (host === "cloudflare") return median(rows(target, n, { host }).map((s) => s.floor).filter((v) => v !== undefined)) ?? 0;
		return floorKey ? quantile(LATENCY[kind](rows(floorKey, n)), 0.5) : undefined;
	};
	/** p50, p95 and max of a latency net of its floor, with the observation count and the floor itself. */
	const latency = (kind, target, n, opts = {}) => {
		const host = opts.host ?? "miniflare";
		const raw = LATENCY[kind](rows(target, n, { ...opts, host }));
		if (raw.length === 0) return undefined;
		const fl = floor(kind, target, n, host);
		const net = raw.map((v) => v - (fl ?? 0));
		return { n: raw.length, floor: fl, p50: quantile(net, 0.5), p95: quantile(net, 0.95), max: Math.max(...net) };
	};
	const metric = (name, target, n, opts) => {
		const [, kind, stat = "p50"] = /^(warm|cold)(?:P(95)|(Max))?$/.exec(name) ?? [];
		if (kind) return latency(kind, target, n, opts)?.[/P95$/.test(name) ? "p95" : /Max$/.test(name) ? "max" : stat];
		return median(rows(target, n, opts).map(perSample[name]).filter((x) => x !== undefined));
	};
	const series = (name, target, opts) => SIZES.map((n) => metric(name, target, n, opts));
	// Building the history: median over the from-zero times of one (target, workload, size) (seedTimes).
	const seedRuns = (target, n, workload = "standard") => seedTimes(seeds, target, n, workload);
	const seed = (target, n, workload = "standard") => median(seedRuns(target, n, workload).map((ms) => ms / 1000));
	const sizesOf = (workload, host = "miniflare") => [...new Set(samples.filter((s) => s.workload === workload && s.host === host).map((s) => s.turns))].sort((a, b) => a - b);
	const targetsOf = (workload, host = "miniflare") => [...new Set(samples.filter((s) => s.workload === workload && s.host === host).map((s) => s.target))];
	// Where-it-runs probe metrics: per sample, then the median over samples. Warm = median of turns 2..n.
	const probeRows = (target, n, { mode = "count", host = "miniflare" } = {}) =>
		(data.probes ?? []).filter((p) => p.target === target && p.turns === n && p.mode === mode && p.host === host);
	const probe = (name, target, n, opts = {}) => {
		const f = probeMetric[name];
		return median(probeRows(target, n, { ...opts, mode: opts.mode ?? (name === "coreWarm" ? "time" : "count") }).map(f).filter((v) => v !== undefined));
	};
	const size = (target) => {
		const z = (data.sizes ?? []).filter((x) => x.target === target);
		return z.length ? z[z.length - 1] : undefined;
	};
	return { rows, metric, latency, series, seed, seedRuns, sizesOf, targetsOf, probeRows, probe, size };
}

/** Isolate memory after a request: JS heap in use + ArrayBuffer backing stores + Wasm linear memory (bytes). */
export const isolateBytes = (x) => x.heapUsed + x.backing + x.wasmBytes;
const warmOf = (p, f) => median(p.turn.slice(1).map(f));
/** Per-probe-sample metrics (RUNNER-SPEC 4a). */
export const probeMetric = {
	cpuWarm: (p) => warmOf(p, (x) => x.cpuMs),
	cpuCold: (p) => p.open.cpuMs + p.turn[0].cpuMs,
	instantiate: (p) => p.open.instantiateMs + p.turn[0].instantiateMs,
	rowsRead: (p) => warmOf(p, (x) => x.rowsRead),
	rowsWritten: (p) => warmOf(p, (x) => x.rowsWritten),
	peakMB: (p) => Math.max(...[p.open, ...p.turn].map(isolateBytes)) / 1e6,
	retainedMB: (p) => (p.retained ? isolateBytes(p.retained) / 1e6 : undefined),
	wasmMB: (p) => Math.max(...[p.open, ...p.turn].map((x) => x.wasmBytes)) / 1e6,
	crossings: (p) => warmOf(p, (x) => x.crossings),
	hopSteps: (p) => warmOf(p, (x) => x.hopSteps),
	coreWarm: (p) => warmOf(p, (x) => x.coreMs),
};

/** Review tables: n, median, p90 and a bootstrap 95% interval per cell, for every metric the slides show. */
export function tables(data) {
	const { meta } = data;
	const q = makeQuery(data);
	const f = (v, d = 0) => (v === undefined ? "–" : v.toLocaleString("en-US", { maximumFractionDigits: d, minimumFractionDigits: d }));
	let md = `# Tables for ${meta.session?.id ?? "unnamed session"} (${meta.status})\n\nSource: results file sha256 \`${data.sha256}\`. warm = every turn after the first of every sample, pooled (n = turns); cold = open + first turn, one per sample (n = samples). p50, p95 and max are net of the floor: the p50 of the empty target's same latency at the same size (Rule A on Miniflare; Rule B, the native build, has no request path and no floor). 95% interval = seeded bootstrap of the net p50 (2,000 rounds).\n\n${Object.entries(RULES).map(([k, v]) => `- ${v}`).join("\n")}\n`;
	if (meta.status === "placeholder") md += "\n> DRAFT. Placeholder data: every value is synthetic, not a measurement. Do not publish.\n";
	else if (meta.status === "draft") md += "\n> DRAFT. Not reviewed. Do not publish.\n";
	const hosts = [...new Set(data.samples.map((s) => s.host))];
	for (const host of hosts) {
		for (const workload of Object.keys(meta.workloads)) {
			const sizes = q.sizesOf(workload, host);
			if (sizes.length === 0) continue;
			md += `\n## ${workload} (${host})\n\n| rule | target | turns | samples | floor warm / cold ms | warm n | warm p50 | warm p95 | warm max | warm p50 95% CI | cold p50 | cold p95 | cold max | DB MB | seed s (runs) |\n|---|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|\n`;
			for (const t of q.targetsOf(workload, host)) {
				for (const n of sizes) {
					const rows = q.rows(t, n, { workload, host });
					if (rows.length === 0) continue;
					const warm = q.latency("warm", t, n, { workload, host });
					const cold = q.latency("cold", t, n, { workload, host });
					const [wl, wh] = bootstrapMedian(LATENCY.warm(rows).map((v) => v - (warm.floor ?? 0)));
					const label = meta.targets[t].kind === "floor" ? `${meta.targets[t].label} (raw)` : `${meta.targets[t].label}${meta.targets[t].build ? ` (${meta.targets[t].build})` : ""}`;
					md += `| ${ruleOf(meta.targets[t])} | ${label} \`${meta.targets[t].version}\` | ${f(n)} | ${rows.length} | ${f(warm.floor, 1)} / ${f(cold.floor, 1)} | ${warm.n} | ${f(warm.p50)} | ${f(warm.p95)} | ${f(warm.max)} | ${f(wl)}-${f(wh)} | ${f(cold.p50)} | ${f(cold.p95)} | ${f(cold.max)} | ${f(median(rows.map(perSample.mb)), 2)} | ${f(q.seed(t, n, workload), 1)} (${q.seedRuns(t, n, workload).length}) |\n`;
				}
			}
		}
	}
	for (const host of ["miniflare", "cloudflare"].filter((h) => (data.probes ?? []).some((p) => p.host === h))) {
		const probes = data.probes.filter((p) => p.host === host);
		md += host === "cloudflare" ? `\n## Where it runs: per-request probe (Cloudflare; bench/cloud.ts)\n\nCount runs only. CPU ms = Cloudflare's CPU time of the Worker invocation plus the Durable Object invocations it caused (wrangler tail). The JS heap is not observable there: peak MB = Wasm memory only.\n\n| target | turns | n | CPU warm ms | CPU cold ms | rows read/turn | rows written/turn | Wasm peak MB | crossings/turn | hop steps/turn |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n` : ""
		if (host === "cloudflare") {
			for (const t of [...new Set(probes.map((p) => p.target))])
				for (const n of [...new Set(probes.filter((p) => p.target === t).map((p) => p.turns))].sort((a, b) => a - b)) {
					const v = (name, d = 1) => f(q.probe(name, t, n, { host }), d);
					md += `| ${meta.targets[t].label}${meta.targets[t].build ? ` (${meta.targets[t].build})` : ""} \`${meta.targets[t].version}\` | ${f(n)} | ${q.probeRows(t, n, { host }).length} | ${v("cpuWarm")} | ${v("cpuCold")} | ${v("rowsRead", 0)} | ${v("rowsWritten", 0)} | ${v("wasmMB")} | ${v("crossings", 0)} | ${v("hopSteps", 0)} |\n`;
				}
			continue;
		}
		md += `\n## Where it runs: per-request probe (Miniflare; RUNNER-SPEC 4a)\n\nMedians over samples of: warm = median of turns 2-10 per sample; cold = open + first turn. Peak MB = largest JS heap + ArrayBuffers + Wasm memory read after any request, before collection, so it counts garbage. Retained MB = the same sum after a full collection after the last turn: what the isolate holds (decimal MB; limit 128 MB). Wasm high-water MB = the largest memory.buffer.byteLength read after any request (Wasm memory never shrinks). All Rule A. Core ms from "time" runs only.\n\n| target | turns | n count | n time | CPU warm ms | CPU cold ms | instantiate ms | rows read/turn | rows written/turn | peak MB (before GC) | retained MB | Wasm high-water MB | crossings/turn | hop steps/turn | core ms/turn |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n`;
		for (const t of [...new Set(probes.map((p) => p.target))])
			for (const n of [...new Set(probes.filter((p) => p.target === t).map((p) => p.turns))].sort((a, b) => a - b)) {
				const v = (name, d = 1) => f(q.probe(name, t, n), d);
				md += `| ${meta.targets[t].label}${meta.targets[t].build ? ` (${meta.targets[t].build})` : ""} \`${meta.targets[t].version}\` | ${f(n)} | ${q.probeRows(t, n).length} | ${q.probeRows(t, n, { mode: "time" }).length} | ${v("cpuWarm")} | ${v("cpuCold")} | ${v("instantiate")} | ${v("rowsRead", 0)} | ${v("rowsWritten", 0)} | ${v("peakMB")} | ${v("retainedMB")} | ${v("wasmMB")} | ${v("crossings", 0)} | ${v("hopSteps", 0)} | ${v("coreWarm")} |\n`;
			}
	}
	if ((data.sizes ?? []).length > 0) {
		md += `\n## Upload size (bench/size.ts)\n\n| target | bytes | of which Wasm | gzip (reference) |\n|---|---:|---:|---:|\n`;
		for (const z of data.sizes) md += `| ${meta.targets[z.target].label}${meta.targets[z.target].build ? ` (${meta.targets[z.target].build})` : ""} | ${f(z.bytes)} | ${f(z.wasm ?? 0)} | ${f(z.gzip)} |\n`;
	}
	return md;
}
