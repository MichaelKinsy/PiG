// Gate and metric tests: node --test test/
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { assemble } from "../lib/assemble.mjs";
import { IDENTITY_ROWS, PROBE_FIELDS, ResultsError, SEED_FINGERPRINTS, SIZES, isolateBytes, makeQuery, parse, probeMetric, roles, seedTimes, validate } from "../lib/results.mjs";
import { renderSlides, verdict } from "../lib/slides.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..");
const CORE = "abcdef1234567";
const SHA = { TinyGo: "a".repeat(64), Go: "b".repeat(64) };

function finalMeta() {
	return {
		t: "meta",
		status: "final",
		session: { id: "s1", interleaved: true },
		machine: { host: "smc1", cpu: "Xeon", pinning: "CPU 23", relative: "about 2x" },
		runner: { repo: "badlogic/durable-bench", commit: "e9abffa" },
		protocol: { samples: 3, turns: 10, seed: "scratch" },
		model: { label: "durable-bench's JS scripted model" },
		contract: { status: "pass", corpus: "full", crash_matrix: "pass", reference: "da866ada", core: CORE, builds: ["TinyGo", "Go"], artifacts: SHA, identity: IDENTITY_ROWS, rows: { ready: 98, skipped: 16, crash_matrix: 7 }, report_url: "https://example.org/contract" },
		targets: {
			"pig-tinygo": { label: "PiG Durable", build: "TinyGo", kind: "pig", role: "headline", version: CORE, artifact: { sha256: SHA.TinyGo, bytes: 1, gzip: 1 } },
			"pi-head": { label: "pi-durable main", kind: "pi", role: "reference", version: "da866ada" },
			tardie: { label: "Tardigrade", kind: "tardigrade", role: "contender", version: "0.44.0" },
			"pig-go": { label: "PiG Durable", build: "Go", kind: "pig", role: "detail", version: CORE, artifact: { sha256: SHA.Go, bytes: 1, gzip: 1 } },
			empty: { label: "Empty target", kind: "floor", role: "floor", version: "floor" },
		},
		workloads: { standard: { title: "standard", description: "as published" } },
	};
}

// Raw durable-bench lines (no t field), as bench/run.ts writes them.
function rawSamples(meta) {
	const out = [];
	for (const target of Object.keys(meta.targets))
		for (const turns of SIZES)
			for (let k = 0; k < 3; k++)
				out.push(
					meta.targets[target].kind === "floor"
						? { target, version: "floor", turns, sample: k, open: 1, turn: [5, ...WARM.map(() => 2)], bytes: 0, rss: 100 }
						: { target, version: meta.targets[target].version, turns, sample: k, open: 10 + k, turn: [100, ...WARM.map((w) => w + k)], bytes: 1e6 * (k + 1), rss: 100 },
				);
	return out;
}
// 34 warm turns per sample, 3 samples: 102 pooled warm turns per cell (MIN_WARM_TURNS 100).
const WARM = Array.from({ length: 34 }, (_, i) => [20, 30, 40][i % 3]);
function rawSeeds(meta) {
	return Object.keys(meta.targets).filter((t) => meta.targets[t].kind !== "floor").flatMap((target) => SIZES.map((turns) => ({ target, version: meta.targets[target].version, turns, seedMs: turns * 10, fingerprint: SEED_FINGERPRINTS.standard[turns], bytes: 1 })));
}
// Raw bench/probe.ts and bench/size.ts lines (kind field) for the headline and reference, as the final gate needs.
const req = (over = {}) => ({ ...Object.fromEntries(PROBE_FIELDS.map((f) => [f, 1])), ...over });
function rawProbes(meta, over = {}) {
	const r = roles(meta);
	const out = [];
	for (const target of [r.headline, r.reference])
		for (const mode of ["count", "time"])
			for (const turns of mode === "count" ? SIZES : [50, 3500]) out.push({ kind: "probe", mode, target, version: meta.targets[target].version, turns, sample: 0, open: req(), turn: [req(), req(), req()], retained: { heapUsed: 1, backing: 1, wasmBytes: 1 }, ...over });
	return out;
}
const rawSizes = (meta) => [roles(meta).headline, roles(meta).reference].map((target) => ({ kind: "size", target, version: meta.targets[target].version, bytes: 2, gzip: 1, wasm: 1 }));
const tagged = (lines) => lines.map(({ kind, ...l }) => ({ t: kind, ...l }));
function finalText(edit = (m) => m) {
	const meta = edit(finalMeta());
	const lines = [meta, ...rawSamples(meta), ...rawSeeds(meta).map((s) => ({ t: "seed", ...s })), ...tagged(rawProbes(meta)), ...tagged(rawSizes(meta))];
	return `${lines.map((l) => JSON.stringify(l)).join("\n")}\n`;
}
const check = (text) => validate(parse(text));
const rejects = (text, pattern) => assert.throws(() => check(text), (e) => e instanceof ResultsError && pattern.test(e.message));

test("a complete final file passes every gate", () => {
	assert.deepEqual(check(finalText()), []);
});

test("metrics follow durable-bench's slide rules", () => {
	const q = makeQuery(parse(finalText()));
	// Floor (empty target): cold 1 + 5 = 6 per sample, warm turns all 2.
	// cold = open + turn[0] per sample -> 110, 111, 112, net 104, 105, 106: p50 105, max 106.
	assert.equal(q.metric("cold", "pi-head", 50), 105);
	assert.equal(q.metric("coldMax", "pi-head", 50), 106);
	// warm = all 102 turns after the first, pooled: per sample 12x(20+k), 11x(30+k), 11x(40+k); net of 2.
	const pool = [0, 1, 2].flatMap((k) => WARM.map((w) => w + k - 2)).sort((a, b) => a - b);
	assert.equal(pool.length, 102);
	const at = (p) => pool[Math.floor(101 * p)] + (pool[Math.ceil(101 * p)] - pool[Math.floor(101 * p)]) * (101 * p - Math.floor(101 * p));
	assert.equal(q.metric("warm", "pi-head", 50), at(0.5));
	assert.equal(q.metric("warmP95", "pi-head", 50), at(0.95));
	assert.equal(q.metric("warmMax", "pi-head", 50), 40);
	assert.deepEqual(q.latency("warm", "pi-head", 50).floor, 2);
	// The floor itself is raw, and the native build (Rule B) has no floor.
	assert.equal(q.latency("warm", "empty", 50).floor, 0);
	assert.equal(q.metric("mb", "pi-head", 50), 2);
	assert.equal(q.seed("pi-head", 3500), 35);
});

test("the committed placeholder file is wholly synthetic and validates", () => {
	const data = parse(readFileSync(join(root, "data/placeholder.jsonl"), "utf8"));
	assert.equal(data.meta.status, "placeholder");
	validate(data);
	assert.ok([...data.samples, ...data.seeds].every((l) => l.placeholder === true));
});

test("PiG numbers need a green CONTRACT gate", () => {
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, status: "pending" } })), /CONTRACT gate/);
	rejects(finalText((m) => ({ ...m, contract: undefined })), /meta.contract is missing/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, crash_matrix: "fail" } })), /crash_matrix/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, corpus: "slice" } })), /full corpus/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, report_url: "" } })), /report_url/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, reference: "7c10bd43" } })), /reference/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, rows: undefined } })), /contract.rows/);
	// A draft is gated too: real PiG numbers never render before the core is green, watermark or not.
	rejects(finalText((m) => ({ ...m, status: "draft", contract: { ...m.contract, status: "pending" } })), /CONTRACT gate/);
});

test("every PiG build is the gated core and a gated build", () => {
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, builds: ["TinyGo"] } })), /build Go did not pass/);
	const other = finalText((m) => {
		m.targets["pig-go"].version = "1111111";
		return m;
	});
	assert.throws(() => check(other), ResultsError);
	const rebuilt = finalText((m) => {
		m.targets["pig-go"].artifact.sha256 = "c".repeat(64);
		return m;
	});
	rejects(rebuilt, /measured module sha256 c+ is not the gated b+/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, artifacts: undefined } })), /contract.artifacts.TinyGo/);
});

test("benchmark-slice prototype cores never render, in any status", () => {
	for (const proto of ["872c81bf5", "407c624b7", "872c81bf5a77"]) {
		const text = finalText((m) => {
			for (const t of Object.values(m.targets)) if (t.kind === "pig") t.version = proto;
			m.contract.core = proto;
			return m;
		});
		rejects(text, /prototype/);
	}
});

test("placeholder and measured lines never mix", () => {
	const lines = finalText().trim().split("\n");
	const withFake = [...lines, JSON.stringify({ ...JSON.parse(lines[1]), placeholder: true })].join("\n");
	rejects(withFake, /placeholder line in a measured results file/);
	const ph = readFileSync(join(root, "data/placeholder.jsonl"), "utf8").trim().split("\n");
	const real = { ...JSON.parse(ph[1]) };
	delete real.placeholder;
	rejects([...ph, JSON.stringify(real)].join("\n"), /only placeholder lines/);
});

test("like-for-like: seeded histories must match durable-bench's fingerprints", () => {
	const lines = finalText().trim().split("\n");
	const i = lines.findIndex((l) => l.includes('"t":"seed"') && l.includes('"target":"pig-tinygo"'));
	const bad = { ...JSON.parse(lines[i]), fingerprint: "0000000000000000" };
	lines[i] = JSON.stringify(bad);
	rejects(lines.join("\n"), /fingerprint 0000000000000000/);
	// All targets agreeing on a different history is still not durable-bench's workload.
	const all = finalText()
		.trim()
		.split("\n")
		.map((l) => (l.includes('"t":"seed"') && l.includes('"turns":50,') ? JSON.stringify({ ...JSON.parse(l), fingerprint: "1111111111111111" }) : l));
	rejects(all.join("\n"), /is not durable-bench's b017b487524e44a4/);
	// A workload without published fingerprints: the targets must still agree with each other.
	const custom = finalText((m) => {
		m.workloads.custom = { title: "custom", description: "no published fingerprints" };
		return m;
	})
		.trim()
		.split("\n");
	custom.push(JSON.stringify({ t: "seed", target: "pi-head", version: "da866ada", workload: "custom", turns: 50, seedMs: 1, fingerprint: "aaaaaaaaaaaaaaaa" }));
	assert.doesNotThrow(() => check(custom.join("\n")));
	custom.push(JSON.stringify({ t: "seed", target: "tardie", version: "0.44.0", workload: "custom", turns: 50, seedMs: 1, fingerprint: "bbbbbbbbbbbbbbbb" }));
	rejects(custom.join("\n"), /differs from pi-head's aaaaaaaaaaaaaaaa/);
});

test("a final file must be complete and interleaved", () => {
	rejects(finalText((m) => ({ ...m, protocol: { ...m.protocol, samples: 4 } })), /protocol needs 4/);
	rejects(finalText((m) => ({ ...m, session: { ...m.session, interleaved: false } })), /interleaved/);
	rejects(finalText((m) => ({ ...m, machine: { ...m.machine, relative: "" } })), /machine.relative/);
	rejects(finalText((m) => ({ ...m, model: undefined })), /model.label/);
	rejects(finalText((m) => ({ ...m, protocol: { ...m.protocol, seed: "ladder" } })), /protocol.seed/);
	rejects(finalText((m) => ({ ...m, runner: { ...m.runner, changes: "TODO patch path" } })), /TODO/);
	// Latencies are net of the empty-target floor, and p50/p95/max need 100 warm turns per cell.
	rejects(finalText((m) => { const { empty, ...targets } = m.targets; return { ...m, targets }; }).replace(/^.*"target":"empty".*\n/gm, ""), /floor target/);
	rejects(finalText().replace(/("target":"tardie"[^\n]*"turns":250[^\n]*"turn":\[100)(,\d+){33}/, "$1"), /tardie standard 250: \d+ warm turns, p50\/p95\/max need 100/);
	rejects(finalText((m) => ({ ...m, targets: { ...m.targets, empty: { ...m.targets.empty, role: "detail" } } })), /floor/);
	// Rows written and the Wasm high-water mark are reported at every size.
	rejects(finalText().replace(/^.*"mode":"count","target":"pi-head"[^\n]*"turns":1000.*\n/m, ""), /pi-head 1000: no where-it-runs probe sample/);
});

test("build.mjs refuses to put a non-final deck into final/ and the reverse", () => {
	const dir = mkdtempSync(join(tmpdir(), "report-"));
	const run = (input, out) => spawnSync(process.execPath, [join(root, "build.mjs"), input, "--out", join(dir, out), "--html-only"], { encoding: "utf8" });
	const ph = run(join(root, "data/placeholder.jsonl"), "final");
	assert.equal(ph.status, 1);
	assert.match(ph.stderr, /must not render into final/);
	const fin = join(dir, "results.jsonl");
	writeFileSync(fin, finalText());
	assert.equal(run(fin, "draft").status, 1);
	const ok = run(fin, "final");
	assert.equal(ok.status, 0, ok.stderr);
	const cover = readFileSync(join(dir, "final/html/01-cover.html"), "utf8");
	assert.ok(!cover.includes("wm-big\">DRAFT"), "final deck has no watermark");
	assert.ok(cover.includes("https://example.org/contract"), "footer links the row-identity verification");
	assert.equal(run(join(root, "data/placeholder.jsonl"), "draft").status, 0);
	const draft = readFileSync(join(dir, "draft/html/01-cover.html"), "utf8");
	assert.ok(draft.includes("wm-big\">DRAFT"), "placeholder deck is watermarked");
});

test("assemble maps durable-bench targets, records sources and stays valid", () => {
	const meta = finalMeta();
	meta.workloads["compact-big"] = { title: "realistic", description: "compaction on" };
	meta.bench_targets = { "pi-head-compact-big": { target: "pi-head", workload: "compact-big" } };
	const raw = rawSamples(meta);
	raw.push({ ...raw[0], target: "pi-head-compact-big", version: "da866ada" });
	const text = assemble(meta, [
		{ file: "a/results.jsonl", kind: "sample", text: raw.map((l) => JSON.stringify(l)).join("\n") },
		...rawSeeds(meta).map((s, i) => ({ file: `f/${i}.json`, kind: "seed", text: JSON.stringify(s) })),
		{ file: "results/probe.jsonl", kind: "probe", text: rawProbes(meta).map((l) => JSON.stringify(l)).join("\n") },
		{ file: "results/size.jsonl", kind: "size", text: rawSizes(meta).map((l) => JSON.stringify(l)).join("\n") },
	]);
	const data = parse(text);
	validate(data);
	assert.equal(data.meta.sources.length, 3 + rawSeeds(meta).length);
	assert.equal(data.probes.length, rawProbes(meta).length);
	assert.ok(data.probes.every((p) => p.t === "probe" && p.kind === undefined && p.host === "miniflare"));
	const mapped = data.samples.filter((s) => s.workload === "compact-big");
	assert.equal(mapped.length, 1);
	assert.equal(mapped[0].target, "pi-head");
	assert.equal(mapped[0].bench_target, "pi-head-compact-big");
	assert.throws(() => assemble(meta, [{ file: "x", kind: "sample", text: JSON.stringify({ ...raw[0], target: "unknown" }) }]), /not mapped/);
});

test("the session meta template carries every field the final gates read", () => {
	const meta = JSON.parse(readFileSync(join(root, "data/session-meta.template.json"), "utf8"));
	const raw = [];
	// As committed, the TODOs fail the gates.
	const sample = (target, version, turns, k) => ({ target, version, turns, sample: k, open: 1, turn: [2, 3, 4, 3, 4, 3, 4, 3, 4, 3], bytes: 1 });
	const build = (m) => {
		for (const [k, t] of Object.entries(m.targets)) for (const n of SIZES) for (let i = 0; i < m.protocol.samples; i++) raw.push(sample(k, t.version, n, i));
		const seeds = Object.entries(m.targets).filter(([, t]) => t.kind !== "floor").flatMap(([k, t]) => SIZES.map((n) => ({ file: `${k}-${n}.json`, kind: "seed", text: JSON.stringify({ target: k, version: t.version, turns: n, seedMs: 1, fingerprint: SEED_FINGERPRINTS.standard[n] }) })));
		const extra = Object.values(m.targets).some((t) => t.kind === "pig") ? [{ file: "probe.jsonl", kind: "probe", text: rawProbes(m).map((l) => JSON.stringify(l)).join("\n") }, { file: "size.jsonl", kind: "size", text: rawSizes(m).map((l) => JSON.stringify(l)).join("\n") }] : [];
		return assemble(m, [{ file: "r.jsonl", kind: "sample", text: raw.map((l) => JSON.stringify(l)).join("\n") }, ...seeds, ...extra]);
	};
	assert.throws(() => validate(parse(build(meta))), ResultsError);
	// With the TODOs filled, the same template passes.
	raw.length = 0;
	const filled = JSON.parse(JSON.stringify(meta).replaceAll('"TODO pass"', '"pass"').replaceAll('"TODO core commit"', `"${CORE}"`).replace('"TODO dcore-integrate commit"', `"${CORE}"`).replace('"TODO https link to the committed CONTRACT report"', '"https://example.org/c"').replace(/"TODO scratch[^"]*"/, '"scratch"').replace(/"[^"]*TODO[^"]*"/g, '"filled"'));
	filled.contract.artifacts = { TinyGo: "a".repeat(64), Go: "b".repeat(64), native: "c".repeat(64) };
	filled.contract.rows = { ready: 98, skipped: 16, crash_matrix: 7 };
	for (const t of Object.values(filled.targets)) if (t.kind === "pig") t.artifact.sha256 = filled.contract.artifacts[t.build];
	assert.deepEqual(validate(parse(build(filled))), []);
});

test("assemble reads durable-repro's run-session and seed-time lines", () => {
	const meta = finalMeta();
	meta.status = "draft";
	meta.contract = undefined;
	meta.bench_targets = { empty: null, "pigwasm-tinygo-sc-js": null };
	for (const k of ["pig-tinygo", "pig-go"]) delete meta.targets[k];
	meta.targets["pig-tinygo"] = { label: "PiG Durable", build: "TinyGo", kind: "pig", role: "headline", version: CORE };
	const run = (target, version, turns) => ({
		session: "s",
		interleaved: true,
		target,
		version,
		turns,
		variant: "",
		samples: [0, 1].map((k) => ({ sample: k, position: k, startupMs: 140, wakeWallMs: 10 + k, turnWallMs: [100, 20, 30, 40], rssMB: 170, stats: { bytes: 2e6 }, gateCpu: 0.05, load1: 60 })),
	});
	const runs = [run("pi-head", "da866ada", 3500), run("tardie", "0.44.0", 3500), run("empty", "empty", 3500), run("pigwasm-tinygo-sc-js", "872c81bf5", 3500)];
	const seedTime = (target, version, range, extra = {}) => ({ kind: "seed-time", target, version, range, from: range.split(":")[0], toTurns: Number(range.split(":")[1]), supported: true, samples: [{ sample: 0, wallMs: 2000 }, { sample: 1, wallMs: 4000 }], ...extra });
	const seeds = [
		seedTime("pi-head", "da866ada", "from50:250"),
		seedTime("pi-head", "da866ada", "empty:250", { fingerprint: SEED_FINGERPRINTS.standard[250] }),
		seedTime("tardie", "0.44.0", "empty:250", { fingerprint: SEED_FINGERPRINTS.standard[250] }),
		seedTime("pigwasm-tinygo-sc-js", "872c81bf5", "empty:250", { supported: false, samples: [] }),
	];
	const text = assemble(meta, [
		{ file: "session-runs.jsonl", kind: "sample", text: runs.map((l) => JSON.stringify(l)).join("\n") },
		{ file: "seed-time.jsonl", kind: "seed", text: seeds.map((l) => JSON.stringify(l)).join("\n") },
	]);
	const data = parse(text);
	validate(data);
	assert.equal(data.samples.length, 4, "two runs x two samples; the floor and the prototype are dropped");
	assert.deepEqual(data.meta.sources.map((s) => [s.lines, s.dropped]), [[4, 2], [4, 1]]);
	assert.ok(!text.includes("872c81bf5"), "a dropped prototype leaves no number in the results file");
	const q = makeQuery(data);
	assert.equal(q.metric("cold", "pi-head", 3500), 110.5); // wake + first turn: 110, 111
	assert.equal(q.metric("warm", "tardie", 3500), 30);
	assert.equal(q.metric("mb", "pi-head", 3500), 2);
	// Building the history counts only runs from an empty store: the from50 increment is not a full build.
	assert.equal(q.seedRuns("pi-head", 250).length, 2);
	assert.equal(q.seed("pi-head", 250), 3);
	// A from-scratch run must prove its history.
	const bad = seeds.slice(1, 2).map((s) => ({ ...s, fingerprint: undefined }));
	assert.throws(() => validate(parse(assemble(meta, [{ file: "r", kind: "sample", text: JSON.stringify(runs[0]) }, { file: "s", kind: "seed", text: JSON.stringify(bad[0]) }]))), /fingerprint required/);
});

test("the contract record is derived from the gate report and refuses anything short of green", async () => {
	const { contractRecord, parseCorpus } = await import("../lib/contract.mjs");
	const corpus = parseCorpus(`[pin]
reference = "earendil-works/pi"
commit = "da866ada17bc78c81855d091ab06ab8fb3b6e16e"

[[scenario]]
id = "E00"
runner = "example"
levels = ["store", "commit"]
status = "ready"

[[scenario]]
id = "E16"
runner = "example"
status = "skipped"

[[scenario]]
id = "H-50"
runner = "matrix"
status = "ready"
${SIZES.map((n) => `\n[[scenario]]\nid = "W1-${n}"\nrunner = "bench"\nstatus = "ready"\n`).join("")}`);
	const w1 = SIZES.map((n) => ({ id: `W1-${n}`, status: "pass" }));
	const run = (status = "pass") => ({ results: [{ id: "E00", status: "pass" }, { id: "E16", status: "skipped" }, { id: "H-50", status }, ...w1] });
	const base = { corpus, builds: { TinyGo: "wasm-tinygo" }, modules: { TinyGo: Buffer.from("wasm") }, core: CORE, reportUrl: "https://example.org/gate" };
	const rec = contractRecord({ ...base, report: { pi: run(), "wasm-tinygo": run() } });
	assert.equal(rec.reference, "da866ada");
	assert.equal(rec.artifacts.TinyGo, createHash("sha256").update("wasm").digest("hex"));
	// The record feeds the final gates as is.
	const meta = finalMeta();
	meta.contract = { ...rec, builds: ["TinyGo", "Go"], artifacts: { ...rec.artifacts, Go: SHA.Go } };
	meta.targets["pig-tinygo"].artifact.sha256 = rec.artifacts.TinyGo;
	assert.doesNotThrow(() => check(finalText(() => meta)));
	for (const [why, report] of [
		["crash matrix pending", { pi: run(), "wasm-tinygo": run("pending") }],
		["crash matrix failed", { pi: run(), "wasm-tinygo": run("fail") }],
		["no reference control", { "wasm-tinygo": run() }],
		["row missing", { pi: run(), "wasm-tinygo": { results: [{ id: "E00", status: "pass" }, ...w1] } }],
		["ready row skipped", { pi: run(), "wasm-tinygo": { results: [{ id: "E00", status: "skipped" }, { id: "H-50", status: "pass" }, ...w1] } }],
	])
		assert.throws(() => contractRecord({ ...base, report }), ResultsError, why);
	assert.throws(() => contractRecord({ ...base, report: { pi: run(), "wasm-tinygo": run() }, reportUrl: "http://x" }), /https/);
	// The candidate memory bound on the reference's own run: identity held (the bound is checked after the trace matched).
	const overBound = { id: "W1-3500", status: "fail", detail: { problem: "memory grows 6.2 MiB per measured turn (rss: ...)", commits: 40 } };
	const piOver = { results: run().results.map((x) => (x.id === "W1-3500" ? overBound : x)) };
	assert.deepEqual(contractRecord({ ...base, report: { pi: piOver, "wasm-tinygo": run() } }).rows.reference_over_memory_bound, ["W1-3500"]);
	assert.throws(() => contractRecord({ ...base, report: { pi: run(), "wasm-tinygo": piOver } }), /wasm-tinygo W1-3500: fail/, "a candidate over the bound fails");
	assert.throws(() => contractRecord({ ...base, report: { pi: { results: run().results.map((x) => (x.id === "W1-3500" ? { ...overBound, detail: { problem: "different change" } } : x)) }, "wasm-tinygo": run() } }), /pi W1-3500: fail/, "a reference identity failure fails");
	// Only the memory bound is excused: any other reference failure that happens to carry a commit count still fails.
	assert.throws(() => contractRecord({ ...base, report: { pi: { results: run().results.map((x) => (x.id === "W1-3500" ? { ...overBound, detail: { problem: "the order differs at commit 12", commits: 40 } } : x)) }, "wasm-tinygo": run() } }), /pi W1-3500: fail/, "only the memory bound is excused");
	// A reference skip excuses a ready row only with the gate's own reason (the reference runs none of its tests here).
	const otherSkip = { results: run().results.map((x) => (x.id === "E00" ? { id: "E00", status: "skipped", detail: "vitest filtered every test" } : x)) };
	assert.throws(() => contractRecord({ ...base, report: { pi: otherSkip, "wasm-tinygo": otherSkip } }), /pi E00: skipped, but the corpus marks it ready/, "a reference skip for another reason is no excuse");
	// crash_matrix "pass" needs every crash-matrix row ready: a pending one is not proven.
	const pendingMatrix = { ...corpus, scenarios: [...corpus.scenarios, { id: "H-250", runner: "matrix", status: "pending" }] };
	assert.throws(() => contractRecord({ ...base, corpus: pendingMatrix, report: { pi: run(), "wasm-tinygo": run() } }), /crash-matrix row H-250 is pending/);
	// A ready row the reference itself runs none of here: skipped everywhere with the gate's reason, and recorded.
	const e2e = (x) => (x.id === "E00" ? { id: "E00", status: "skipped", detail: "the reference passes no test of e2e.test.ts here (1 skipped)" } : x);
	const skippedRun = { results: run().results.map(e2e) };
	assert.deepEqual(contractRecord({ ...base, report: { pi: skippedRun, "wasm-tinygo": skippedRun } }).rows.skipped_by_reference, ["E00"]);
	assert.throws(() => contractRecord({ ...base, report: { pi: run(), "wasm-tinygo": skippedRun } }), /wasm-tinygo E00: skipped/, "only when the reference skipped it");
	// The shape `contract all` writes: a failed build is named, and failing rows come before rows that did not run.
	const partial = { pi: { results: [{ id: "E00", status: "pass" }] }, "wasm-tinygo": { built: "build `make x` failed (2): no rule", results: [{ id: "E00", status: "pending" }] } };
	assert.throws(() => contractRecord({ ...base, report: partial }), (e) => /wasm-tinygo: build `make x` failed[\s\S]*wasm-tinygo E00: pending[\s\S]*pi: 5 of 6 ready rows did not run \(H-50, W1-50/.test(e.message));
});

test("workload slides print the fixture's entries, transcript bytes and compactions under each size", async () => {
	const { renderSlides } = await import("../lib/slides.mjs");
	const data = parse(readFileSync(join(root, "data/placeholder.jsonl"), "utf8"));
	validate(data);
	const slides = renderSlides(data, { fonts: { inter: "/f/inter.woff2", mono: "/f/mono.woff2" } });
	const html = slides.find((s) => s.name === "workload-compact-big").html;
	for (const note of ["169 entries", "0.2 MB", "11.8k entries", "15.4 MB", "69 compactions", "4 compactions"]) assert.ok(html.includes(`>${note}</text>`), note);
	assert.ok(!html.includes(">0 compactions<"), "a history without compactions prints no compaction line");
	const standard = slides.find((s) => s.name === "latency").html;
	assert.ok(!standard.includes(" entries</text>"), "slides without fixture facts print no notes");
	const bad = parse(readFileSync(join(root, "data/placeholder.jsonl"), "utf8"));
	bad.meta.workloads.big.fixtures[250] = { entries: -1, entry_bytes: 1 };
	assert.throws(() => validate(bad), /fixtures.250 needs whole-number entries/);
});

test("assemble takes fixture facts from pi-durable main's run-session lines only", () => {
	const meta = finalMeta();
	meta.status = "draft";
	meta.contract = undefined;
	for (const k of ["pig-tinygo", "pig-go"]) delete meta.targets[k];
	meta.targets["pig-tinygo"] = { label: "PiG Durable", build: "TinyGo", kind: "pig", role: "headline", version: CORE };
	meta.workloads.big = { title: "big", description: "multi-KB tool results" };
	const fixture = (entries) => ({ path: "fixtures/x", meta: { entries, entryBytes: entries * 1400, compactions: 0, activeEntries: entries } });
	const run = (target, version, f) => ({ session: "s", target, version, turns: 1000, variant: "big", fixture: f, samples: [{ sample: 0, wakeWallMs: 10, turnWallMs: [100, 20, 30], stats: { bytes: 1 } }] });
	const text = (lines) => [{ file: "runs", kind: "sample", text: lines.map((l) => JSON.stringify(l)).join("\n") }];
	// run-session.ts copies pi-head's fixture metadata into other targets' lines; only the reference's counts.
	const data = parse(assemble(meta, text([run("tardie", "0.44.0", fixture(9)), run("pi-head", "da866ada", fixture(3335))])));
	assert.deepEqual(data.meta.workloads.big.fixtures, { 1000: { entries: 3335, entry_bytes: 4669000, compactions: 0, active_entries: 3335 } });
	assert.equal(meta.workloads.big.fixtures, undefined, "the caller's meta is not modified");
	assert.throws(() => assemble(meta, text([run("pi-head", "da866ada", fixture(3335)), run("pi-head", "da866ada", fixture(3336))])), /fixture facts for big at 1000 turns differ/);
});

test("building the history: from-scratch runs, else fingerprinted chains from an empty store", () => {
	const seg = (from, turns, seedMs, extra = {}) => ({ target: "t", workload: "standard", from, turns, seedMs, fingerprint: SEED_FINGERPRINTS.standard[turns], ...extra });
	const ladder = (pass, ms) => [seg("empty", 50, ms, { pass }), seg("50", 250, ms, { pass }), seg("250", 1000, ms, { pass }), seg("1000", 3500, ms, { pass })];
	const seeds = [...ladder(1, 10), ...ladder(2, 20)];
	assert.deepEqual(seedTimes(seeds, "t", 3500), [40, 80]);
	assert.deepEqual(seedTimes(seeds, "t", 250), [20, 40]);
	// A from-scratch run to the size wins over chains.
	assert.deepEqual(seedTimes([...seeds, seg("empty", 3500, 7)], "t", 3500), [7]);
	// A gap or an unproved segment breaks that pass's chain; durable-repro's `from250` spelling and `sample` work as well.
	assert.deepEqual(seedTimes(ladder(1, 10).filter((s) => s.turns !== 250), "t", 3500), []);
	assert.deepEqual(seedTimes(ladder(1, 10).map((s) => (s.turns === 1000 ? { ...s, fingerprint: undefined } : s)), "t", 3500), []);
	assert.deepEqual(seedTimes([seg("empty", 250, 5, { sample: 0 }), seg("from250", 1000, 6, { sample: 0 })], "t", 1000), [11]);
	assert.throws(() => seedTimes([...ladder(1, 10), seg("50", 250, 3, { pass: 1 })], "t", 3500), /two segments start at 50/);
	// A final file may time the history by chains.
	const chained = (m) => {
		const lines = [{ ...m, protocol: { ...m.protocol, seed: "chain" } }, ...rawSamples(m), ...tagged(rawProbes(m)), ...tagged(rawSizes(m))];
		for (const target of Object.keys(m.targets)) for (const [i, n] of SIZES.entries()) lines.push({ t: "seed", target, version: m.targets[target].version, from: i ? String(SIZES[i - 1]) : "empty", turns: n, seedMs: 100, pass: 1, fingerprint: SEED_FINGERPRINTS.standard[n] });
		return `${lines.map((l) => JSON.stringify(l)).join("\n")}\n`;
	};
	assert.deepEqual(check(chained(finalMeta())), []);
	assert.equal(makeQuery(parse(chained(finalMeta()))).seed("pi-head", 3500), 0.4);
	rejects(finalText((m) => ({ ...m, protocol: { ...m.protocol, seed: "ladder" } })), /protocol.seed/);
});

test("the builds slide: TinyGo, Go, native of the gated core; the TS core only as a labelled design control", () => {
	const withControl = (edit = (t) => t) => (m) => ({ ...m, targets: { ...m.targets, "ts-control": edit({ label: "Durable core", build: "TS", kind: "control", role: "control", version: "de1fac200" }) } });
	// The control is not the gated core and needs no gated module.
	assert.deepEqual(check(finalText(withControl())), []);
	rejects(finalText(withControl((t) => ({ ...t, kind: "pig" }))), /design control/);
	rejects(finalText(withControl((t) => ({ ...t, version: "872c81bf5" }))), /prototype/);
	// native runs on host native, and nothing else does.
	const native = finalText((m) => {
		m.targets["pig-native"] = { label: "PiG Durable", build: "native", kind: "pig", role: "detail", version: CORE, model: "Go port of the scripted model", artifact: { sha256: "c".repeat(64), bytes: 1, gzip: 1 } };
		m.contract = { ...m.contract, builds: [...m.contract.builds, "native"], artifacts: { ...m.contract.artifacts, native: "c".repeat(64) } };
		return m;
	});
	assert.match(native, /"target":"pig-native"/);
	rejects(native, /only the native build runs on host native/); // rawSamples writes no host, so they default to miniflare
	assert.deepEqual(check(native.replace(/("target":"pig-native")/g, '$1,"host":"native"')), []);
	rejects(finalText().replace('"target":"pig-go"', '"target":"pig-go","host":"native"'), /only the native build/);
	rejects(native.replace(',"model":"Go port of the scripted model"', ""), /names the model it ran/);
	// Every number carries its rule: the native build is Rule B (no floor), the rest Rule A, and a mixed slide says which.
	const data = parse(native.replace(/("target":"pig-native")/g, '$1,"host":"native"'));
	const q = makeQuery(data);
	assert.equal(q.latency("warm", "pig-native", 50, { host: "native" }).floor, 0);
	assert.equal(q.latency("warm", "pig-go", 50).floor, 2);
	const slides = renderSlides(data, { fonts: { inter: join(root, "fonts.css"), mono: join(root, "fonts.css") } });
	const html = (n) => slides.find((x) => x.name === n).html;
	assert.match(html("builds"), /PiG Durable \(native\) <span class="muted mono">abcdef1234567<\/span> <span class="muted">· Rule B<\/span>/);
	assert.match(html("builds"), /PiG Durable \(Go\) <span class="muted mono">abcdef1234567<\/span> <span class="muted">· Rule A<\/span>/);
	for (const n of ["cover", "latency", "storage", "growth", "where-it-runs"]) {
		assert.match(html(n), /<span class="rule">Rule A<\/span><\/h1>/, n);
		assert.doesNotMatch(html(n), /Rule B/, n);
	}
	// Latency: p50 net of the floor as bars, p95 whiskers, and p50/p95/max at 3,500 turns.
	assert.match(html("latency"), /Net of the floor: an empty target on the same request path \(at 3,500: warm 2\.0 ms, cold 6\.0 ms\)/);
	assert.match(html("latency"), /<td style="color:[^"]+">pi-durable main<\/td><td class="muted">102<\/td>/);
	// Rows written and the Wasm high-water mark at every size, the latter against the 128 MB limit.
	assert.match(html("growth"), /128 MB isolate limit/);
});

test("per-size row identity names the gate's W1 rows", () => {
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, identity: undefined } })), /identity.standard.50/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, identity: { standard: { ...IDENTITY_ROWS.standard, 3500: "W1-1000" } } } })), /not the gate row/);
	rejects(finalText((m) => ({ ...m, contract: { ...m.contract, identity: { ...IDENTITY_ROWS, big: { 50: "W4c-50" } } } })), /no bench row for this workload/);
});

test("where it runs: probe lines are gated like every PiG line, and the final deck must answer the question", () => {
	// The final gate: every headline/reference probe cell and both upload sizes.
	rejects(finalText().split("\n").filter((l) => !l.includes('"t":"size"')).join("\n"), /no upload size line/);
	rejects(finalText().split("\n").filter((l) => !(l.includes('"t":"probe"') && l.includes('"mode":"time"'))).join("\n"), /BENCH_PROBE=time/);
	// Only targets that run in a Durable Object; whole probe records; the PiG gate covers probe lines.
	const add = (line) => `${finalText()}${JSON.stringify(line)}\n`;
	const meta = finalMeta();
	rejects(add({ t: "probe", mode: "count", target: "tardie", version: "0.44.0", turns: 50, open: req(), turn: [req(), req()] }), /pi-durable main or a Wasm build/);
	rejects(add({ t: "probe", mode: "count", target: "pig-tinygo", version: CORE, turns: 50, open: req(), turn: [req({ cpuMs: undefined }), req()] }), /each need/);
	// The memory row needs the retained reading on the headline's and reference's count probes.
	rejects(finalText().replace(/,"retained":\{[^}]*\}/, ""), /without the retained-memory reading/);
	rejects(add({ t: "probe", mode: "count", target: "pig-tinygo", version: CORE, turns: 50, open: req(), turn: [req(), req()], retained: { heapUsed: 1 } }), /retained needs/);
	rejects(add({ t: "probe", mode: "warm", target: "pig-tinygo", version: CORE, turns: 50, open: req(), turn: [req(), req()] }), /probe mode/);
	rejects(add({ t: "probe", placeholder: true, mode: "count", target: "pig-tinygo", version: CORE, turns: 50, open: req(), turn: [req(), req()] }), /placeholder line in a measured/);
	const draft = { ...meta, status: "draft", contract: undefined };
	rejects([draft, ...tagged(rawProbes(meta))].map((l) => JSON.stringify(l)).join("\n"), /meta.contract is missing/);
	// Assemble keeps only bench/probe.ts and bench/size.ts lines in those inputs.
	assert.throws(() => assemble(meta, [{ file: "p", kind: "probe", text: JSON.stringify(rawSizes(meta)[0]) }]), /not a bench\/probe.ts line/);
});

test("where it runs: memory sums heap, buffers and Wasm; warm metrics skip the first turn", () => {
	assert.equal(isolateBytes({ heapUsed: 1, backing: 2, wasmBytes: 4 }), 7);
	const p = { open: req({ heapUsed: 10e6, cpuMs: 5 }), turn: [req({ cpuMs: 100, rowsRead: 900 }), req({ cpuMs: 3, rowsRead: 10 }), req({ cpuMs: 5, rowsRead: 20, wasmBytes: 30e6 })] };
	assert.equal(probeMetric.cpuWarm(p), 4);
	assert.equal(probeMetric.cpuCold(p), 105);
	assert.equal(probeMetric.rowsRead(p), 15);
	assert.equal(probeMetric.peakMB(p), (1 + 1 + 30e6) / 1e6);
});

test("where it runs: every loss is flagged, and no win under 1.3x is claimed", async () => {
	assert.deepEqual(verdict(10, 10.2), { kind: "tie", ratio: 10 / 10.2 });
	assert.equal(verdict(12, 10).kind, "lose");
	assert.equal(verdict(1, 0).kind, "lose"); // pi-durable has no JS<->Wasm crossings
	assert.equal(verdict(9, 10).kind, "slight");
	assert.equal(verdict(5, 10).kind, "win");
	assert.equal(verdict(undefined, 10).kind, "none");
	// A latency net of the floor below zero is not a measurement to rank.
	assert.equal(verdict(-1, 10).kind, "none");
	assert.equal(verdict(10, -1).kind, "none");
	// Rendered: PiG worse on memory and crossings, better on CPU; the heading lists exactly the losses.
	const meta = finalMeta();
	const text = finalText((m) => m).split("\n").filter((l) => !l.includes('"t":"probe"')).join("\n");
	const probes = rawProbes(meta).map((l) => {
		const pig = l.target === "pig-tinygo";
		const x = () => req({ cpuMs: pig ? 2 : 8, crossings: pig ? 1300 : 0, wasmBytes: pig ? 9e6 : 0 });
		return { ...l, open: x(), turn: [x(), x(), x()], retained: { heapUsed: 1, backing: 1, wasmBytes: pig ? 9e6 : 0 } };
	});
	const data = parse(`${text}${tagged(probes).map((l) => JSON.stringify(l)).join("\n")}\n`);
	validate(data);
	const fonts = { inter: join(root, "fonts.css"), mono: join(root, "fonts.css") };
	const html = renderSlides(data, { fonts }).find((s) => s.name === "where-it-runs").html;
	assert.match(html, /We lose on memory, JS↔Wasm crossings\./);
	assert.match(html, /we win: 4\.0× at 50, 4\.0× at 3,500/);
	assert.match(html, /we lose: pi-durable has none/);
	assert.doesNotMatch(html, /We lose on [^.]*CPU/);
	// Memory is judged on what the isolate retains after GC; garbage before collection shows in brackets only.
	const same = parse(`${text}${tagged(probes.map((l) => ({ ...l, retained: { heapUsed: 1, backing: 1, wasmBytes: 1 } }))).map((l) => JSON.stringify(l)).join("\n")}\n`);
	const html2 = renderSlides(same, { fonts }).find((s) => s.name === "where-it-runs").html;
	assert.match(html2, /We lose on JS↔Wasm crossings\./);
	assert.match(html2, /Retained memory/);
});

test("where it runs on Cloudflare: count probes only, no invented heap, a measured account, and memory is not a verdict", async () => {
	const meta = finalMeta();
	const cfReq = (over = {}) => req({ heapUsed: null, heapTotal: null, backing: null, ...over });
	const cfProbe = (target, turns, over = {}) => ({ t: "probe", host: "cloudflare", mode: "count", target, version: meta.targets[target].version, turns, sample: 0, open: cfReq(over), turn: [cfReq(over), cfReq(over), cfReq(over)] });
	const add = (...lines) => `${finalText((m) => ({ ...m, cloudflare: { status: "measured", label: "Workers Paid, colo X." } }))}${lines.map((l) => JSON.stringify(l)).join("\n")}\n`;
	check(add(cfProbe("pig-tinygo", 50)));
	rejects(add({ ...cfProbe("pig-tinygo", 50), open: req() }), /null \(not observable on Cloudflare\)/);
	rejects(add({ ...cfProbe("pig-tinygo", 50), mode: "time" }), /only count probes run there/);
	rejects(`${finalText()}${JSON.stringify(cfProbe("pig-tinygo", 50))}\n`, /Cloudflare samples need meta.cloudflare/);
	assert.equal(probeMetric.wasmMB({ open: cfReq({ wasmBytes: 2e6 }), turn: [cfReq({ wasmBytes: 5e6 }), cfReq({ wasmBytes: 3e6 })] }), 5);
	// Rendered: a second slide; PiG's Wasm memory against pi-durable's none is shown, not counted as a loss.
	const lines = [];
	for (const t of ["pig-tinygo", "pi-head"]) for (const n of [50, 3500]) lines.push(cfProbe(t, n, { cpuMs: t === "pi-head" ? 8 : 2, crossings: t === "pi-head" ? 0 : 1300, wasmBytes: t === "pi-head" ? 0 : 9e6 }));
	const data = parse(add(...lines));
	validate(data);
	const fonts = { inter: join(root, "fonts.css"), mono: join(root, "fonts.css") };
	const slides = renderSlides(data, { fonts });
	const cf = slides.find((s) => s.name === "where-it-runs-cloudflare").html;
	assert.match(cf, /We lose on JS↔Wasm crossings\./);
	assert.match(cf, /Measured on Cloudflare: Workers Paid, colo X\./);
	assert.match(cf, /no Wasm/);
	assert.doesNotMatch(cf, /Time inside the core/);
	assert.match(slides.find((s) => s.name === "where-it-runs").html, /Cloudflare on the next slide/);
	assert.ok(slides.findIndex((s) => s.name === "where-it-runs-cloudflare") === slides.findIndex((s) => s.name === "where-it-runs") + 1);
});

test("the cover's ratios follow the deck verdict: a tie within 3%, no win under 1.3x", () => {
	// PiG TinyGo at 3,500 turns: seeds 1.2x faster than pi-durable main, half the database, warm and cold the same.
	const text = finalText()
		.replace(/("t":"seed","target":"pig-tinygo","version":"abcdef1234567","turns":3500,"seedMs":)35000/, "$129000")
		.replace(/("target":"pig-tinygo"[^\n]*"turns":3500[^\n]*"bytes":)(\d+)/g, (_, head, bytes) => `${head}${Number(bytes) / 2}`);
	const data = parse(text);
	assert.deepEqual(validate(data), []);
	const cover = renderSlides(data, { fonts: { inter: join(root, "fonts.css"), mono: join(root, "fonts.css") } }).find((s) => s.name === "cover").html;
	assert.match(cover, /<span class="muted">slightly faster than pi-durable main \(1\.21×\)<\/span>/);
	assert.doesNotMatch(cover, /class="good">1\.2× faster/);
	assert.match(cover, /<span class="good">2\.0× smaller than pi-durable main<\/span>/);
	assert.equal((cover.match(/about the same as pi-durable main/g) ?? []).length, 2, "warm and cold");
});
