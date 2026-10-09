#!/usr/bin/env node
// Writes a wholly synthetic results file for the dry-run render: `node placeholder.mjs > data/placeholder.jsonl`.
// Every value is invented from a smooth curve plus seeded jitter. None is a measurement, none is copied from a
// prototype run, and every line carries "placeholder": true, which forces the DRAFT watermark (lib/results.mjs).
import { SEED_FINGERPRINTS, SIZES } from "./lib/results.mjs";

let x = 42;
const rand = () => {
	x = (x + 0x6d2b79f5) >>> 0;
	let t = x;
	t = Math.imul(t ^ (t >>> 15), t | 1);
	t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
	return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const jitter = (v, spread = 0.12) => +(v * (1 + (rand() - 0.3) * spread)).toFixed(3);

const meta = {
	t: "meta",
	status: "placeholder",
	session: { id: "placeholder", date: "YYYY-MM-DD", interleaved: true, rounds: 12 },
	machine: {
		host: "smc1",
		cpu: "Intel Xeon Platinum 8462Y+",
		pinning: "one pinned logical core (CPU 23) of a shared 128-thread host",
		relative: "Absolute times run about 2× Mario's i9-13900 numbers; compare only within this session.",
	},
	runner: { repo: "badlogic/durable-bench", commit: "e9abffa", changes: "CPU gate reads the pinned core and its SMT sibling only; PiG targets added." },
	protocol: { samples: 12, turns: 10, seed: "scratch", cpu_gate: "<= 8% over 1 s on CPU 23 and 87" },
	model: { label: "durable-bench's own scripted model in JS (walks the whole transcript on every call), pi-ai's faux provider and the same lookup tool, for every target." },
	contract: { status: "pending", reference: "da866ada" },
	cloudflare: { status: "placeholder", label: "PLACEHOLDER: Workers Paid account, SQLite-backed Durable Objects, region and client location to be recorded." },
	targets: {
		"pig-tinygo": { label: "PiG Durable", build: "TinyGo", kind: "pig", role: "headline", version: "placeholder", artifact: { bytes: 1_000_000, gzip: 400_000 } },
		"pi-head": { label: "pi-durable main", kind: "pi", role: "reference", version: "da866ada" },
		tardie: { label: "Tardigrade", kind: "tardigrade", role: "contender", version: "0.44.0" },
		"pig-go": { label: "PiG Durable", build: "Go", kind: "pig", role: "detail", version: "placeholder", artifact: { bytes: 5_000_000, gzip: 1_300_000 } },
		"pig-native": { label: "PiG Durable", build: "native", kind: "pig", role: "detail", version: "placeholder", model: "PLACEHOLDER: the Go port of durable-bench's scripted model, in process", artifact: { bytes: 9_000_000, gzip: 3_500_000 } },
		"ts-control": { label: "Durable core", build: "TS", kind: "control", role: "control", version: "placeholder" },
		empty: { label: "Empty target", kind: "floor", role: "floor", version: "floor" },
	},
	workloads: {
		standard: { title: "durable-bench as published", description: "Compaction off, 256 B tool results." },
		"compact-big": {
			title: "Realistic: compaction on, multi-KB tool results",
			description: "Stores built with compaction on (131,072-token window) and 4-64 KiB tool results, so the model sees a bounded window while the database keeps growing. Tardigrade has no equivalent stores; it is not in this chart.",
			// Real facts of the pi-durable-main-written fixtures (bench/durable/fixtures/README.md), not measurements.
			fixtures: {
				50: { entries: 169, entry_bytes: 218221, compactions: 0, active_entries: 169 },
				250: { entries: 843, entry_bytes: 1094214, compactions: 4, active_entries: 233 },
				1000: { entries: 3373, entry_bytes: 4387680, compactions: 19, active_entries: 233 },
				3500: { entries: 11807, entry_bytes: 15382125, compactions: 69, active_entries: 233 },
			},
		},
		big: {
			title: "Realistic: multi-KB tool results, no compaction",
			description: "Every tool result 4 KiB, every 13th 16 KiB, every 97th 64 KiB; the whole history stays in the model context.",
			fixtures: {
				50: { entries: 169, entry_bytes: 218137, compactions: 0, active_entries: 169 },
				250: { entries: 835, entry_bytes: 1148846, compactions: 0, active_entries: 835 },
				1000: { entries: 3335, entry_bytes: 4726340, compactions: 0, active_entries: 3335 },
				3500: { entries: 11669, entry_bytes: 16708856, compactions: 0, active_entries: 11669 },
			},
		},
	},
	text: {},
	publish: { results_path: "bench/durable/report/data/placeholder.jsonl" },
};

// Invented curves: c = cold ms, w = warm ms, mb = MB after the turns, s = seed seconds, all as a + b * turns.
const curves = {
	"pi-head": { c: [120, 0.07], w: [65, 0.025], mb: [0.2, 0.0027], s: [0.4, 0.011] },
	tardie: { c: [330, 0.8], w: [130, 0.07], mb: [0.4, 0.0079], s: [0.5, 0.085] },
	"pig-tinygo": { c: [60, 0.01], w: [30, 0.004], mb: [0.2, 0.0027], s: [0.3, 0.007] },
	"pig-go": { c: [110, 0.02], w: [35, 0.005], mb: [0.2, 0.0027], s: [0.3, 0.008] },
	"pig-native": { c: [20, 0.004], w: [12, 0.002], mb: [0.2, 0.0027], s: [0.2, 0.004] },
	"ts-control": { c: [80, 0.03], w: [40, 0.008], mb: [0.2, 0.0027], s: [0.3, 0.009] },
	empty: { c: [9, 0], w: [3, 0], mb: [0, 0], s: [0, 0] },
};
const lines = [meta];
const sample = (target, n, workload, host, k, scale = 1) => {
	const cv = curves[target];
	const cold = (cv.c[0] + cv.c[1] * n) * scale;
	const warm = (cv.w[0] + cv.w[1] * n) * scale;
	const turn = [jitter(cold * 0.9), ...Array.from({ length: 9 }, () => jitter(warm))];
	return {
		t: "sample",
		placeholder: true,
		target,
		version: meta.targets[target].version,
		workload,
		host,
		turns: n,
		sample: k,
		startup: jitter(140),
		open: jitter(cold * 0.1),
		turn,
		rss: jitter(170),
		bytes: Math.round((cv.mb[0] + cv.mb[1] * n) * (workload === "standard" ? 1 : 2.2) * 1e6),
		cpu: 0.05,
		load: 50,
	};
};
const seed = (target, n, workload) => {
	const cv = curves[target];
	return {
		t: "seed",
		placeholder: true,
		target,
		version: meta.targets[target].version,
		workload,
		turns: n,
		seedMs: Math.round((cv.s[0] + cv.s[1] * n) * 1000 * (workload === "standard" ? 1 : 1.6)),
		fingerprint: SEED_FINGERPRINTS[workload][n],
	};
};

for (const t of Object.keys(curves).filter((t) => t !== "empty")) for (const n of SIZES) lines.push(seed(t, n, "standard"));
for (const w of ["compact-big", "big"]) for (const t of ["pig-tinygo", "pi-head"]) for (const n of SIZES) lines.push(seed(t, n, w));
// One interleaved session: round-robin over targets within each size and round.
for (let round = 0; round < meta.protocol.samples; round++)
	for (const n of SIZES) {
		const order = Object.keys(curves);
		for (let i = 0; i < order.length; i++) {
			const t = order[(i + round) % order.length];
			lines.push(sample(t, n, "standard", meta.targets[t].build === "native" ? "native" : "miniflare", round));
		}
		for (const w of ["compact-big", "big"])
			for (const t of ["pig-tinygo", "pi-head"]) lines.push(sample(t, n, w, "miniflare", round, w === "big" ? 1.5 : 0.9));
	}
for (let k = 0; k < 5; k++) for (const n of [50, 3500]) for (const t of ["pig-tinygo", "pi-head", "tardie"]) lines.push(sample(t, n, "standard", "cloudflare", k, 0.6));

// Where-it-runs probe and upload sizes (RUNNER-SPEC 4a), equally invented, for the Durable Object targets.
const probeCurves = {
	"pi-head": { rowsRead: [1200, 0.03], heap: [9e6, 3000], wasm: [0, 0], crossings: 0, hops: 0, core: 0, inst: 0 },
	"pig-tinygo": { rowsRead: [500, 0], heap: [15e6, 1500], wasm: [6e6, 1500], crossings: 1300, hops: 310, core: 0.5, inst: 3 },
	"pig-go": { rowsRead: [500, 0], heap: [15e6, 1500], wasm: [12e6, 2500], crossings: 1300, hops: 310, core: 0.55, inst: 20 },
};
const probe = (target, n, mode, k) => {
	const cv = curves[target];
	const pc = probeCurves[target];
	const req = (ms, first) => ({
		ms: jitter(ms),
		cpuMs: jitter(ms * 0.95),
		crossings: Math.round(jitter(pc.crossings * (first ? 0.1 : 1), 0.02)),
		exportCalls: pc.crossings,
		importCalls: 0,
		hopSteps: Math.round(pc.hops * (first ? 0.1 : 1)),
		coreMs: mode === "time" ? jitter(ms * pc.core) : 0,
		instantiateMs: first ? pc.inst : 0,
		rowsRead: Math.round(pc.rowsRead[0] + pc.rowsRead[1] * n),
		rowsWritten: 869,
		wasmBytes: Math.round(pc.wasm[0] + pc.wasm[1] * n),
		heapUsed: Math.round(jitter(pc.heap[0] + pc.heap[1] * n)),
		heapTotal: Math.round(1.4 * (pc.heap[0] + pc.heap[1] * n)),
		backing: 1_000_000,
	});
	const cold = cv.c[0] + cv.c[1] * n;
	const warm = cv.w[0] + cv.w[1] * n;
	return { t: "probe", placeholder: true, mode, target, version: meta.targets[target].version, host: "miniflare", turns: n, sample: k, open: req(cold * 0.1, true), turn: [req(cold * 0.9, false), ...Array.from({ length: 9 }, () => req(warm, false))], retained: { heapUsed: Math.round(0.6 * (pc.heap[0] + pc.heap[1] * n)), backing: 200_000, wasmBytes: Math.round(pc.wasm[0] + pc.wasm[1] * n) } };
};
for (let k = 0; k < 3; k++) for (const n of SIZES) for (const t of Object.keys(probeCurves)) for (const mode of ["count", "time"]) lines.push(probe(t, n, mode, k));
// Cloudflare probe lines (bench/cloud.ts): count runs only, no observable heap.
const cfProbe = (target, n, k) => {
	const x = probe(target, n, "count", k);
	const strip = (q) => ({ ...q, cpuMs: q.cpuMs * 0.7, heapUsed: null, heapTotal: null, backing: null });
	const { retained: _, ...rest } = x;
	return { ...rest, host: "cloudflare", open: strip(x.open), turn: x.turn.map(strip) };
};
for (const [t, bytes, wasm] of [["pi-head", 900_000, 0], ["pig-tinygo", 1_700_000, 750_000], ["pig-go", 6_300_000, 5_300_000]]) lines.push({ t: "size", placeholder: true, target: t, version: meta.targets[t].version, bytes, wasm, gzip: Math.round(bytes * 0.27) });

for (let k = 0; k < 3; k++) for (const n of [50, 3500]) for (const t of ["pi-head", "pig-tinygo"]) lines.push(cfProbe(t, n, k));
process.stdout.write(`${lines.map((l) => JSON.stringify(l)).join("\n")}\n`);
