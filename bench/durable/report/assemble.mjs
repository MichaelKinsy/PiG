#!/usr/bin/env node
// Assembles the one committed results file from a final session's raw outputs, then validates it with the same gates
// the renderer applies. Nothing is written when a gate fails.
//
// usage: node assemble.mjs --meta session-meta.json --out data/results.jsonl
//          --samples <durable-bench results.jsonl>... [--seeds <fixture .json>...] [--cloudflare <results.jsonl>...]
//          [--probes <results/probe.jsonl>...] [--sizes <results/size.jsonl>...]   (RUNNER-SPEC 4a)
//
// durable-bench target names map to (target, workload) through meta.bench_targets, e.g.
//   "pi-head-compact-big": { "target": "pi-head", "workload": "compact-big" }
// A name that is itself a key of meta.targets maps to (name, "standard").
import { readFileSync, writeFileSync } from "node:fs";
import { assemble } from "./lib/assemble.mjs";
import { ResultsError, parse, validate } from "./lib/results.mjs";

const args = process.argv.slice(2);
const lists = { "--samples": [], "--seeds": [], "--cloudflare": [], "--probes": [], "--sizes": [] };
let metaPath;
let outPath;
for (let i = 0, cur; i < args.length; i++) {
	const a = args[i];
	if (a === "--meta") metaPath = args[++i];
	else if (a === "--out") outPath = args[++i];
	else if (a in lists) cur = lists[a];
	else if (cur) cur.push(a);
	else {
		console.error(`unexpected argument ${a}`);
		process.exit(2);
	}
}
if (!metaPath || !outPath || lists["--samples"].length === 0) {
	console.error("usage: node assemble.mjs --meta <meta.json> --out <results.jsonl> --samples <file>... [--seeds <file>...] [--cloudflare <file>...] [--probes <file>...] [--sizes <file>...]");
	process.exit(2);
}

try {
	const meta = JSON.parse(readFileSync(metaPath, "utf8"));
	const read = (kind) => (file) => ({ file, kind, text: readFileSync(file, "utf8") });
	const text = assemble(meta, [...lists["--samples"].map(read("sample")), ...lists["--cloudflare"].map(read("cloudflare")), ...lists["--seeds"].map(read("seed")), ...lists["--probes"].map(read("probe")), ...lists["--sizes"].map(read("size"))]);
	const data = parse(text);
	for (const w of validate(data)) console.warn(`warning: ${w}`);
	writeFileSync(outPath, text);
	console.log(`${outPath}: ${data.samples.length} samples, ${data.seeds.length} seeds, ${data.probes.length} probes, ${data.sizes.length} sizes, status ${data.meta.status}, sha256 ${data.sha256}`);
} catch (e) {
	if (!(e instanceof ResultsError)) throw e;
	console.error(`not written: ${e.message}`);
	process.exit(1);
}
