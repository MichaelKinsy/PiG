// Merges a session's raw outputs into the one results file format (../README.md). Accepted inputs:
//   samples: durable-bench bench/run.ts lines (one per sample, `turn[]`), or durable-repro run-session.ts lines
//            (one per target and size, `samples[]` with `wakeWallMs`, `turnWallMs[]`, `stats.bytes`);
//   seeds:   durable-bench fixture JSON files (`seedMs`, `fingerprint`), or durable-repro seed-time.ts lines
//            (`kind: "seed-time"`, `range` "<from>:<turns>", `samples[].wallMs`).
// meta.bench_targets maps a runner target name to { target, workload }; null drops that target (a floor or a
// prototype that must not reach the results file). A name that is itself a key of meta.targets maps to itself.
// The workload defaults to the line's `variant`, else "standard".
// Fixture facts (entries, transcript bytes, compactions, active context range) come from the `fixture.meta` of the
// reference target's run-session lines only: pi-durable main wrote the fixtures, and run-session.ts copies pi-head's
// fixture metadata into other targets' lines (RUNNER-SPEC section 7, point 4). They land in meta.workloads[w].fixtures.
import { basename } from "node:path";
import { ResultsError, sha256 } from "./results.mjs";

const jsonLines = (text, file) => {
	const trimmed = text.trim();
	if (trimmed === "") return [];
	try {
		const one = JSON.parse(trimmed);
		if (one && typeof one === "object" && !Array.isArray(one)) return [one];
	} catch {}
	return trimmed.split("\n").map((l, i) => {
		try {
			return JSON.parse(l);
		} catch (e) {
			throw new ResultsError(`${file}:${i + 1}: not JSON (${e.message})`);
		}
	});
};

export function assemble(meta, inputs) {
	const map = (d, where) => {
		const name = d.target;
		const m = meta.bench_targets && name in meta.bench_targets ? meta.bench_targets[name] : meta.targets[name] ? { target: name } : undefined;
		if (m === undefined) throw new ResultsError(`${where}: runner target ${name} is not mapped (meta.bench_targets; null drops it)`);
		if (m === null) return null;
		return { target: m.target, workload: m.workload ?? (d.variant || "standard"), bench_target: name };
	};
	const sources = [];
	const lines = [];
	const fixtures = {};
	const fixtureFacts = (d, m, where) => {
		const f = d.fixture?.meta;
		if (!f || meta.targets[m.target]?.role !== "reference") return;
		const facts = { entries: f.entries, entry_bytes: f.entryBytes, compactions: f.compactions, active_entries: f.activeEntries };
		const cell = (fixtures[m.workload] ??= {});
		const seen = cell[d.turns];
		if (seen && JSON.stringify(seen) !== JSON.stringify(facts)) throw new ResultsError(`${where}: fixture facts for ${m.workload} at ${d.turns} turns differ from an earlier line`);
		cell[d.turns] = facts;
	};
	for (const { file, text, kind } of inputs) {
		const source = { file: basename(file), kind, sha256: sha256(text), lines: 0, dropped: 0 };
		sources.push(source);
		// The native build runs as a native process, not in a Durable Object; its lines carry host "native".
		const hostOf = (m) => (kind === "cloudflare" ? "cloudflare" : meta.targets[m.target]?.build === "native" ? "native" : "miniflare");
		for (const [i, d] of jsonLines(text, file).entries()) {
			const where = `${file}:${i + 1}`;
			source.lines++;
			const m = map(d, where);
			if (m === null) {
				source.dropped++;
				continue;
			}
			if (kind === "probe" || kind === "size") {
				if (d.kind !== kind) throw new ResultsError(`${where}: not a bench/${kind}.ts line (kind ${JSON.stringify(d.kind)})`);
				const { kind: _, ...rest } = d;
				lines.push({ t: kind, ...rest, target: m.target, bench_target: m.bench_target, ...(kind === "probe" ? { host: rest.host ?? "miniflare" } : {}) });
				continue;
			}
			if (kind === "seed") {
				if (d.kind === "seed-time") {
					if (d.supported === false) continue;
					const [from, to] = String(d.range ?? "").split(":");
					if (Number(to) !== d.toTurns) throw new ResultsError(`${where}: range ${d.range} disagrees with toTurns ${d.toTurns}`);
					for (const s of d.samples ?? [])
						lines.push({ t: "seed", target: m.target, workload: m.workload, bench_target: m.bench_target, version: d.version, turns: d.toTurns, from, seedMs: s.wallMs, fingerprint: s.fingerprint ?? d.fingerprint, sample: s.sample, session: d.session, at: s.at });
				} else lines.push({ t: "seed", ...d, target: m.target, workload: m.workload, bench_target: m.bench_target });
				continue;
			}
			if (Array.isArray(d.samples)) {
				fixtureFacts(d, m, where);
				for (const s of d.samples)
					lines.push({
						t: "sample",
						target: m.target,
						workload: m.workload,
						bench_target: m.bench_target,
						host: hostOf(m),
						version: d.version,
						turns: d.turns,
						session: d.session,
						sample: s.sample,
						position: s.position,
						startup: s.startupMs,
						open: s.wakeWallMs,
						turn: s.turnWallMs,
						rss: s.rssMB,
						bytes: s.stats?.bytes,
						cpu: s.gateCpu,
						load: s.load1,
						at: s.at,
					});
				continue;
			}
			if (d.t && d.t !== "sample") throw new ResultsError(`${where}: unexpected line type ${d.t}`);
			lines.push({ t: "sample", ...d, target: m.target, workload: m.workload, bench_target: m.bench_target, host: hostOf(m) });
		}
	}
	const workloads = Object.fromEntries(Object.entries(meta.workloads ?? {}).map(([w, def]) => [w, fixtures[w] ? { ...def, fixtures: { ...def.fixtures, ...fixtures[w] } } : def]));
	const head = { ...meta, t: "meta", workloads, sources };
	return `${[head, ...lines].map((l) => JSON.stringify(l)).join("\n")}\n`;
}
