// Builds the slide HTML (laid out at 1600x900, rendered at 1.25x to 2000x1125) from a validated results file.
// The visual style (palette, type sizes, stat cards, grouped bars) follows Mario Zechner's pi-vs-tardigrade deck in
// badlogic/durable-bench. That repository has no license, so no code is taken from it; this file is written here.
import { pathToFileURL } from "node:url";
import { LIMITS, SIZES, makeQuery, roles, ruleOf } from "./results.mjs";

export const WIDTH = 1600;
export const HEIGHT = 900;

const C = {
	bg: "#0b0f17",
	panel: "#121826",
	line: "#243046",
	text: "#e6edf3",
	muted: "#8b98a8",
	pi: "#38bdf8",
	tardie: "#fb923c",
	pig: "#a78bfa",
	detail: ["#f472b6", "#facc15", "#2dd4bf"],
	control: "#94a3b8",
	good: "#4ade80",
	bad: "#f87171",
};

const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
// digits "auto": one decimal below 10, none above (seed times run from under a second to minutes).
export const fmt = (v, digits = 0) => {
	if (v === undefined || Number.isNaN(v)) return "–";
	const d = digits === "auto" ? (Math.abs(v) < 10 ? 1 : 0) : digits;
	return v.toLocaleString("en-US", { maximumFractionDigits: d, minimumFractionDigits: d });
};

/**
 * Compares a PiG value with pi-durable main's where lower is better. Within `tie` (relative) is a tie; pi-durable at 0
 * and PiG above it is a loss with no finite ratio (JS<->Wasm crossings: pi-durable has none).
 */
export function verdict(pig, pi, tie = 0.03) {
	if (pig === undefined || pi === undefined) return { kind: "none" };
	// A latency net of the floor can fall below zero on a noisy run; no ratio of such values is a measurement.
	if (pig < 0 || pi < 0) return { kind: "none" };
	if (pi === 0) return pig === 0 ? { kind: "tie" } : { kind: "lose", ratio: Number.POSITIVE_INFINITY };
	const ratio = pig / pi;
	if (Math.abs(ratio - 1) <= tie) return { kind: "tie", ratio };
	if (ratio > 1) return { kind: "lose", ratio };
	// The deck claims no win below 1.3x (README review rule); a smaller lead is reported as "slightly better".
	return 1 / ratio >= WIN_MIN ? { kind: "win", ratio: 1 / ratio } : { kind: "slight", ratio: 1 / ratio };
}
export const WIN_MIN = 1.3;

export function renderSlides(data, { fonts }) {
	const { meta } = data;
	const q = makeQuery(data);
	const r = roles(meta);
	const T = meta.targets;
	const ref = T[r.reference];
	const color = (k) => {
		const role = T[k].role;
		if (role === "headline") return C.pig;
		if (role === "reference") return C.pi;
		if (role === "contender") return C.tardie;
		if (role === "control") return C.control;
		return C.detail[r.detail.indexOf(k) % C.detail.length];
	};
	const name = (k) => (T[k].role === "control" ? `${T[k].label} (${T[k].build}), design control` : T[k].build ? `${T[k].label} (${T[k].build})` : T[k].label);
	// The native build runs as a native process; every other target runs in a Durable Object on Miniflare.
	const localHost = (k) => (T[k].build === "native" ? "native" : "miniflare");
	const draft = meta.status !== "final";
	const watermark =
		meta.status === "placeholder" ? "DRAFT · placeholder data, not measurements · do not publish" : meta.status === "draft" ? "DRAFT · not reviewed · do not publish" : "";
	const contract = meta.contract ?? {};
	const contractOk = contract.status === "pass";
	const machine = meta.machine ?? {};

	const css = `
@font-face { font-family: Inter; src: url(${pathToFileURL(fonts.inter)}) format("woff2"); font-weight: 100 900; }
@font-face { font-family: Mono; src: url(${pathToFileURL(fonts.mono)}) format("woff2"); font-weight: 100 900; }
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { width: ${WIDTH}px; height: ${HEIGHT}px; overflow: hidden; background: ${C.bg}; color: ${C.text}; font-family: Inter, sans-serif; }
body { padding: 52px 72px 20px; display: flex; flex-direction: column; gap: 24px; position: relative; }
h1 { font-size: 46px; font-weight: 800; letter-spacing: -0.02em; line-height: 1.1; }
h2 { font-size: 22px; font-weight: 500; color: ${C.muted}; line-height: 1.4; }
.row { display: flex; gap: 28px; flex: 1; min-height: 0; }
.col { display: flex; flex-direction: column; gap: 16px; flex: 1; min-width: 0; }
.panel { background: ${C.panel}; border: 1px solid ${C.line}; border-radius: 18px; padding: 24px 30px; }
.muted { color: ${C.muted}; } .good { color: ${C.good}; } .bad { color: ${C.bad}; }
.foot { font-size: 15px; color: ${C.muted}; line-height: 1.45; }
.mono { font-family: Mono, monospace; }
ul { list-style: none; display: flex; flex-direction: column; gap: 11px; }
li { font-size: 19px; line-height: 1.36; padding-left: 26px; position: relative; }
li::before { content: ""; position: absolute; left: 4px; top: 10px; width: 8px; height: 8px; border-radius: 50%; background: currentColor; opacity: 0.6; }
a { color: inherit; }
.legend { display: flex; gap: 26px; font-size: 18px; font-weight: 600; flex-wrap: wrap; }
.dot { display: inline-block; width: 14px; height: 14px; border-radius: 4px; margin-right: 8px; vertical-align: -1px; }
.honest { margin-top: auto; flex-shrink: 0; font-size: 13.5px; line-height: 1.5; color: ${C.muted}; border-top: 1px solid ${C.line}; padding-top: 10px; }
.honest b { color: ${C.text}; font-weight: 600; }
.rule { display: inline-block; font-size: 17px; font-weight: 700; letter-spacing: 0.02em; color: ${C.muted}; border: 1.5px solid ${C.line}; border-radius: 7px; padding: 2px 10px; margin-left: 14px; vertical-align: 8px; }
.mini { width: 100%; border-collapse: collapse; font-size: 15px; font-variant-numeric: tabular-nums; }
.mini th, .mini td { padding: 1px 8px; text-align: right; border-top: 1px solid ${C.line}; }
.mini th { color: ${C.muted}; font-weight: 600; } .mini td:first-child, .mini th:first-child { text-align: left; }
.wm { position: absolute; pointer-events: none; }
.wm-big { left: 0; right: 0; top: 270px; text-align: center; font-size: 300px; font-weight: 900; letter-spacing: 0.08em; color: rgba(248,113,113,0.13); transform: rotate(-16deg); }
.wm-tag { top: 16px; right: 72px; font-size: 15px; font-weight: 800; letter-spacing: 0.04em; color: ${C.bg}; background: ${C.bad}; padding: 5px 12px; border-radius: 6px; }
`;

	// Every number is tagged with its rule (results.mjs RULES): slides of one rule carry it in the title, and a slide
	// mixing rules tags each series in the legend.
	const rule = (keys) => [...new Set(keys.map((k) => ruleOf(T[k])))].sort().map((x) => `<span class="rule">Rule ${x}</span>`).join("");
	const legend = (keys) => {
		const mixed = new Set(keys.map((k) => ruleOf(T[k]))).size > 1;
		return `<div class="legend">${keys
			.map((k) => `<span><span class="dot" style="background:${color(k)}"></span>${esc(name(k))} <span class="muted mono">${esc(T[k].version)}</span>${mixed ? ` <span class="muted">· Rule ${ruleOf(T[k])}</span>` : ""}</span>`)
			.join("")}</div>`;
	};

	const honest = (machineLine, modelNote = "") => {
		const link = contractOk ? `<span class="mono">${esc(contract.report_url)}</span>` : `<span class="bad">pending: the production core has not passed the CONTRACT gate</span>`;
		return `<div class="honest">
<div><b>Machine:</b> ${machineLine ?? `${esc(machine.host ?? "?")}, ${esc(machine.cpu ?? "?")}, ${esc(machine.pinning ?? "?")}. ${esc(machine.relative ?? "")}`}</div>
<div><b>Same rows:</b> PiG Durable ${contractOk ? `writes the same rows as pi-durable main <span class="mono">${esc(ref.version)}</span> (${contract.rows ? `all ${fmt(contract.rows.ready - (contract.rows.skipped_by_reference?.length ?? 0))} runnable corpus rows` : "full corpus"} + crash matrix)` : "row identity"}: ${link}</div>
<div><b>Same model (Rule A):</b> ${esc(meta.model?.label ?? "?")}${modelNote ? ` ${esc(modelNote)}` : ""}${meta.session?.id ? ` · session <span class="mono">${esc(meta.session.id)}</span>` : ""}</div>
</div>`;
	};

	const page = (slug, body, machineLine, modelNote) =>
		`<!doctype html><html><head><meta charset="utf-8"><title>${esc(slug)}</title><style>${css}</style></head><body data-slide="${esc(slug)}">${body}${honest(machineLine, modelNote)}${
			draft ? `<div class="wm wm-big">DRAFT</div><div class="wm wm-tag">${esc(watermark)}</div>` : ""
		}</body></html>`;

	// Grouped bars in an SVG viewBox: x groups are history sizes, one bar per series, each bar labelled with its value.
	// A missing value draws a dash on the baseline instead of a bar, so an absent measurement is never a zero.
	// `notes` gives each x group short lines printed under its label (fixture entries and bytes).
	// A series' `hi` values (p95) draw a whisker above its bar; `limit` draws a dashed line with its label.
	function bars({ width, height, labels, notes = [], series, unit = "", digits = 0, axis = "turns of history", limit }) {
		const noteLines = Math.max(0, ...notes.map((n) => n.length));
		const frame = { x0: 16, x1: width - 16, top: 34, base: height - 58 - (noteLines ? 8 + 17 * noteLines : 0) };
		const known = series.flatMap((s) => [...s.values, ...(s.hi ?? [])]).filter((v) => v !== undefined);
		if (limit) known.push(limit.value);
		const scale = (frame.base - frame.top) / ((known.length ? Math.max(...known) : 1) * 1.12 || 1);
		const slot = (frame.x1 - frame.x0) / labels.length;
		const step = Math.min(64, (slot * 0.78) / series.length);
		const bar = step - 6;
		const size = series.length > 3 ? 15 : 18;
		const text = (x, y, fill, body, extra = "") => `<text x="${x}" y="${y}" fill="${fill}" text-anchor="middle" font-family="Inter" ${extra}>${body}</text>`;
		const parts = [`<line x1="${frame.x0}" x2="${frame.x1}" y1="${frame.base}" y2="${frame.base}" stroke="${C.line}" stroke-width="2"/>`];
		for (const [i, label] of labels.entries()) {
			const centre = frame.x0 + slot * (i + 0.5);
			const left = centre - (series.length * step) / 2 + 3;
			let prev; // the previous bar label in this group: a label that would collide with it moves up above it
			for (const [j, s] of series.entries()) {
				const v = s.values[i];
				const mid = left + j * step + bar / 2;
				if (v === undefined) {
					parts.push(text(mid, frame.base - 10, C.muted, "–", `font-size="${size}"`));
					continue;
				}
				const h = Math.max(v * scale, 2);
				parts.push(`<rect x="${left + j * step}" y="${frame.base - h}" width="${bar}" height="${h}" rx="${Math.min(6, bar / 3)}" fill="${s.color}"/>`);
				const hi = s.hi?.[i];
				const top = hi !== undefined && hi > v ? Math.max(hi * scale, h) : h;
				if (top > h) parts.push(`<path d="M${mid} ${frame.base - h}V${frame.base - top}M${mid - bar / 4} ${frame.base - top}H${mid + bar / 4}" stroke="${s.color}" stroke-width="2.5" fill="none"/>`);
				const body = `${fmt(v, digits)}${unit}`;
				const half = (body.length * size * 0.62) / 2;
				let y = frame.base - top - 10;
				if (prev && mid - half < prev.x + prev.half && Math.abs(y - prev.y) < size * 1.3) y = prev.y - size * 1.3;
				prev = { x: mid, half, y };
				parts.push(text(mid, y, s.color, body, `font-size="${size}" font-weight="700"`));
			}
			parts.push(text(centre, frame.base + 32, C.text, label, `font-size="20" font-weight="600"`));
			for (const [k, line] of (notes[i] ?? []).entries()) parts.push(text(centre, frame.base + 54 + 17 * k, C.muted, esc(line), `font-size="13"`));
		}
		if (limit) {
			const y = frame.base - limit.value * scale;
			parts.push(`<line x1="${frame.x0}" x2="${frame.x1}" y1="${y}" y2="${y}" stroke="${C.bad}" stroke-width="2" stroke-dasharray="8 6"/>`);
			// At the left end: the smallest history's bars are the shortest, so the label stays clear of the bar labels.
			parts.push(`<text x="${frame.x0}" y="${y - 8}" fill="${C.bad}" text-anchor="start" font-family="Inter" font-size="16" font-weight="600">${esc(limit.label)}</text>`);
		}
		parts.push(text(width / 2, height - 4, C.muted, axis, `font-size="16"`));
		return `<svg viewBox="0 0 ${width} ${height}" style="width:100%;height:auto;max-height:100%;display:block" xmlns="http://www.w3.org/2000/svg">${parts.join("")}</svg>`;
	}

	const panel = (title, chart) => `<div class="panel col"><div style="font-size:24px;font-weight:700">${title}</div>${chart}</div>`;
	// Entries, transcript bytes and compactions of the history each size starts from, when the results file has them.
	const fixtureNotes = (workload, sizes) =>
		sizes.map((n) => {
			const f = meta.workloads[workload]?.fixtures?.[n];
			if (!f) return [];
			const notes = [`${f.entries < 10000 ? fmt(f.entries) : `${fmt(f.entries / 1000, 1)}k`} entries`, `${fmt(f.entry_bytes / 1e6, 1)} MB`];
			if (f.compactions > 0) notes.push(`${fmt(f.compactions)} compaction${f.compactions === 1 ? "" : "s"}`);
			return notes;
		});
	// Latency charts (warm, cold) draw the p50 net of the floor as the bar and the p95 as its whisker.
	const latencyChart = (name) => name === "warm" || name === "cold";
	const chart = (metricName, keys, { sizes = SIZES, workload = "standard", host, unit = "", digits = 0, width = 680, height = 510, notes = [] } = {}) =>
		bars({
			width,
			height,
			notes,
			labels: sizes.map((n) => fmt(n)),
			series: keys.map((k) => ({
				color: color(k),
				values: sizes.map((n) => (metricName === "seed" ? q.seed(k, n, workload) : q.metric(metricName, k, n, { workload, host: host ?? localHost(k) }))),
				hi: latencyChart(metricName) ? sizes.map((n) => q.metric(`${metricName}P95`, k, n, { workload, host: host ?? localHost(k) })) : undefined,
			})),
			unit,
			digits,
		});
	// Probe charts (where-it-runs count runs, every size).
	const probeChart = (metricName, keys, { digits = 0, limit, width = 680, height = 470 } = {}) =>
		bars({ width, height, labels: SIZES.map((n) => fmt(n)), series: keys.map((k) => ({ color: color(k), values: SIZES.map((n) => q.probe(metricName, k, n)) })), digits, limit });
	// p50 / p95 / max at one size, one row per target.
	const spread = (metricName, keys, n = 3500, opts = {}) =>
		`<table class="mini"><tr><th>at ${fmt(n)} turns, ms</th><th>n</th><th>p50</th><th>p95</th><th>max</th></tr>${keys
			.map((k) => {
				const l = q.latency(metricName, k, n, { host: localHost(k), ...opts });
				return `<tr><td style="color:${color(k)}">${esc(name(k))}</td>${l ? `<td class="muted">${fmt(l.n)}</td><td>${fmt(l.p50)}</td><td>${fmt(l.p95)}</td><td>${fmt(l.max)}</td>` : `<td colspan="4" class="muted">–</td>`}</tr>`;
			})
			.join("")}</table>`;
	const floorNote = () => {
		if (!r.floor) return "Not net of a floor: this file has no empty target.";
		const w = q.latency("warm", r.floor, 3500);
		const c = q.latency("cold", r.floor, 3500);
		return `Net of the floor: an empty target on the same request path (at 3,500: warm ${fmt(w?.p50, 1)} ms, cold ${fmt(c?.p50, 1)} ms).`;
	};
	const last = (metricName, k) => (metricName === "seed" ? q.seed(k, 3500) : q.metric(metricName, k, 3500));
	// The cover follows the same verdict as the where-it-runs slide: within 3% is a tie, and no lead under 1.3x is a win.
	const relative = (pig, pi, kind) => {
		const v = verdict(pig, pi);
		if (v.kind === "none" || !Number.isFinite(v.ratio)) return `<span class="muted">–</span>`;
		if (v.kind === "tie") return `<span class="muted">about the same as pi-durable main</span>`;
		const [better, worse] = kind === "size" ? ["smaller", "larger"] : ["faster", "slower"];
		if (v.kind === "lose") return `<span class="muted">${fmt(v.ratio, 1)}× ${worse} than pi-durable main</span>`;
		if (v.kind === "slight") return `<span class="muted">slightly ${better} than pi-durable main (${fmt(v.ratio, 2)}×)</span>`;
		return `<span class="good">${fmt(v.ratio, 1)}× ${better} than pi-durable main</span>`;
	};

	const contenders = [r.headline, r.reference, r.contender];
	const slides = [];

	// 1. Cover: four stat cards at 3,500 turns.
	{
		const card = (label, metricName, unit, digits, kind) => {
			const [g, p, t] = contenders.map((k) => last(metricName, k));
			return `<div class="panel col" style="gap:10px">
		<div style="font-size:20px;font-weight:600" class="muted">${label}</div>
		<div style="font-size:54px;font-weight:800;letter-spacing:-0.02em;color:${C.pig}">${fmt(g, digits)}<span style="font-size:28px">${unit}</span></div>
		<div style="font-size:22px;font-weight:600;color:${C.pi}">pi-durable main ${fmt(p, digits)}${unit}</div>
		<div style="font-size:22px;font-weight:600;color:${C.tardie}">Tardigrade ${fmt(t, digits)}${unit}</div>
		<div style="font-size:18px">${relative(g, p, kind)}</div></div>`;
		};
		slides.push({
			name: "cover",
			html: page(
				"cover",
				`<h1>PiG Durable, pi-durable and Tardigrade<br>on durable-bench, at 3,500 turns${rule(contenders)}</h1>
		<h2>${esc(meta.text?.cover ?? `PiG Durable is a Go implementation of pi-durable that runs inside a Durable Object as WebAssembly (${T[r.headline].build} build). Same scripted agent, model and tool for everyone, 8 tool calls per turn, SQLite-backed Durable Objects on Miniflare.`)}</h2>
		${legend(contenders)}
		<div class="row" style="flex:0 0 auto">
			${card("Warm turn p50 (thread already open)", "warm", " ms", 0, "time")}
			${card("Cold start p50 (open + first turn)", "cold", " ms", 0, "time")}
			${card("SQLite database", "mb", " MB", 1, "size")}
			${card("Building the history", "seed", " s", "auto", "time")}
		</div>
		<div class="foot">${esc(name(r.headline))} core <span class="mono">${esc(T[r.headline].version)}</span>; pi-durable main <span class="mono">${esc(ref.version)}</span> with its own Durable Object SQLite adapter; Tardigrade ${esc(T[r.contender].version)}. Latencies: p50 net of the empty-target floor; warm over every turn after the first in ${esc(meta.protocol?.samples ?? "?")} fresh runtimes per size (${esc((meta.protocol?.turns ?? 10) - 1)} each), cold over the runtimes; one interleaved session. All three build the same transcript: equal durable-bench context fingerprints at every size. "Building the history" is each system seeding its own 3,500 turns by real turns.</div>`,
			),
		});
	}

	// 2. Latency as conversations grow.
	slides.push({
		name: "latency",
		html: page(
			"latency",
			`<h1>Latency as conversations grow${rule(contenders)}</h1>
	<h2>Warm: one more 8-tool turn on an open thread. Cold: open the agent in a fresh runtime, then the first turn. Bars p50, whiskers p95. ${esc(floorNote())} Lower is better.</h2>
	${legend(contenders)}
	<div class="row">
		${panel("Warm turn, ms", chart("warm", contenders, { height: 400 }) + spread("warm", contenders))}
		${panel("Cold start, ms", chart("cold", contenders, { height: 400 }) + spread("cold", contenders))}
	</div>`,
		),
	});

	// 3. Storage and building the history.
	slides.push({
		name: "storage",
		html: page(
			"storage",
			`<h1>Database size and building the history${rule(contenders)}</h1>
	<h2>SQLite bytes after the measured turns (PiG's index tables included), and the time each system took to build its own history by real turns. Lower is better.</h2>
	${legend(contenders)}
	<div class="row">
		${panel("SQLite database, MB", chart("mb", contenders, { digits: 1 }))}
		${panel("Building the history, s", chart("seed", contenders, { digits: "auto" }))}
	</div>`,
		),
	});

	// 3a. Rows written and the Wasm memory high-water mark at every size (where-it-runs count probes).
	{
		const probed = [r.headline, r.reference, ...r.detail].filter((k) => (data.probes ?? []).some((p) => p.target === k && p.host === "miniflare" && p.mode === "count"));
		const wasm = probed.filter((k) => T[k].kind === "pig");
		if (probed.length > 0)
			slides.push({
				name: "growth",
				html: page(
					"growth",
					`<h1>Rows written and Wasm memory as conversations grow${rule(probed)}</h1>
	<h2>SQLite rows written per warm turn (Durable Objects bill them), and the largest Wasm memory the core reached: memory.buffer.byteLength after every request; it never shrinks. Lower is better. Tardigrade is not probed.</h2>
	${legend(probed)}
	<div class="row">
		${panel("SQLite rows written per warm turn", probeChart("rowsWritten", probed))}
		${panel("Wasm memory high-water mark, MB <span class=\"muted\" style=\"font-size:18px;font-weight:500\">(pi-durable runs no Wasm)</span>", probeChart("wasmMB", wasm, { digits: 1, limit: { value: LIMITS.memoryBytes / 1e6, label: "128 MB isolate limit (JS heap included)" } }))}
	</div>`,
				),
			});
	}

	// 4. Realistic workloads (compaction on, multi-KB tool results), one slide per workload.
	for (const [w, def] of Object.entries(meta.workloads)) {
		if (w === "standard") continue;
		const sizes = q.sizesOf(w);
		if (sizes.length === 0) continue;
		const keys = [r.headline, r.reference, r.contender, ...r.detail].filter((k) => q.targetsOf(w).includes(k));
		slides.push({
			name: `workload-${w}`,
			html: page(
				`workload-${w}`,
				`<h1>${esc(def.title)}${rule(keys)}</h1>
		<h2>${esc(def.description)}</h2>
		${legend(keys)}
		<div class="row">
			${panel("Warm turn, ms", chart("warm", keys, { sizes, workload: w, notes: fixtureNotes(w, sizes) }))}
			${panel("Cold start, ms", chart("cold", keys, { sizes, workload: w, notes: fixtureNotes(w, sizes) }))}
		</div>${def.note ? `<div class="foot">${esc(def.note)}</div>` : ""}`,
			),
		});
	}

	// 5. PiG builds (detail).
	if (r.detail.length > 0) {
		const builds = [r.headline, ...r.detail];
		const keys = [...builds, ...r.control, r.reference];
		const sizes = (k) => (T[k].artifact ? `${T[k].build} ${fmt(T[k].artifact.bytes / 1e6, 2)} MB (${fmt(T[k].artifact.gzip / 1e6, 2)} MB gzip)` : undefined);
		const art = builds.map(sizes).filter(Boolean);
		const ways = ["one", "two", "three", "four", "five"][builds.length - 1] ?? String(builds.length);
		const notes = [];
		if (art.length) notes.push(`Module size: ${art.map(esc).join(" · ")}.`);
		for (const k of builds.filter((k) => T[k].build === "native"))
			notes.push(`Rule B: the native build runs as a native process with its own SQLite, not in a Durable Object${T[k].model ? `, with ${esc(T[k].model)}` : ""}, and has no request path, so no floor: read it as a harness floor, the cost of WebAssembly and the host, not as a deployment.`);
		for (const k of r.control) notes.push(`${esc(name(k))} <span class="mono">${esc(T[k].version)}</span>: the gate's TypeScript design control, not PiG Durable; shown for scale only.`);
		slides.push({
			name: "builds",
			html: page(
				"builds",
				`<h1>One PiG core, ${builds.length} builds${rule(keys)}</h1>
		<h2>${esc(meta.text?.builds ?? `The same core (${contract.core ?? T[r.headline].version}) built ${ways} ways; the rows are the same, only the runtime cost differs. pi-durable main for scale.`)}</h2>
		${legend(keys)}
		<div class="row">
			${panel("Warm turn, ms", chart("warm", keys, { height: 380 }))}
			${panel("Cold start, ms", chart("cold", keys, { height: 380 }))}
		</div>${notes.length ? `<div class="foot">${notes.join(" ")}</div>` : ""}`,
				undefined,
				builds.some((k) => T[k].build === "native" && T[k].model) ? "Exception: the native build, Rule B (note above)." : "",
			),
		});
	}

	// 5a. Where TinyGo runs: the owner's question, "are we at any disadvantage to pi-durable on a Durable Object?".
	// pi-durable main and the headline build on the same Durable Object class; every loss is flagged in red.
	// The Miniflare slide always (with probes); a second one from bench/cloud.ts lines when Cloudflare was measured.
	for (const host of ["miniflare", "cloudflare"]) {
		if (!(data.probes ?? []).some((x) => x.target === r.headline && x.host === host)) continue;
		const cfh = host === "cloudflare";
		const [g, p] = [r.headline, r.reference];
		const sizes = [50, 3500];
		const mb = (b) => (b === undefined ? undefined : b / 1e6);
		const rows = [
			{ label: "Cold start", short: "cold start", sub: cfh ? "open + first turn, at the client" : "p50 net of floor: instantiate + open + turn", unit: "ms", get: (k, n) => q.metric("cold", k, n, { host }) },
			{ label: "Warm turn", short: "warm turn", sub: cfh ? "8 tool calls, at the client" : "p50 net of floor, 8 tool calls", unit: "ms", get: (k, n) => q.metric("warm", k, n, { host }) },
			{ label: "CPU per warm turn", short: "CPU per turn", sub: cfh ? "Cloudflare CPU time: Worker + object" : "workerd CPU time of the request", unit: "ms", digits: 1, get: (k, n) => q.probe("cpuWarm", k, n, { host }), limit: LIMITS.cpuFreeMs, limitText: `${fmt(LIMITS.cpuFreeMs)} ms (Free)` },
			{ label: "CPU, cold start", short: "cold-start CPU", sub: "open + first turn", unit: "ms", digits: 1, get: (k, n) => q.probe("cpuCold", k, n, { host }), limit: LIMITS.cpuFreeMs, limitText: `${fmt(LIMITS.cpuFreeMs)} ms (Free)` },
			{ label: "SQLite rows read", short: "rows read", sub: "per warm turn", unit: "", get: (k, n) => q.probe("rowsRead", k, n, { host }), limitText: "billed" },
			{ label: "SQLite rows written", short: "rows written", sub: "per warm turn", unit: "", get: (k, n) => q.probe("rowsWritten", k, n, { host }), limitText: "billed" },
			cfh
				? { label: "Peak Wasm memory", short: "Wasm memory", sub: "JS heap not observable on Cloudflare", unit: "MB", digits: 1, get: (k, n) => (T[k].kind === "pi" ? undefined : q.probe("wasmMB", k, n, { host })), limit: LIMITS.memoryBytes / 1e6, limitText: "128 MB", info: true, none: "no Wasm" }
				: { label: "Retained memory", short: "memory", sub: "after GC: JS heap + buffers + Wasm (before GC)", unit: "MB", digits: 1, get: (k, n) => q.probe("retainedMB", k, n), extra: (k, n) => { const v = q.probe("peakMB", k, n); return v === undefined ? "" : ` (${fmt(v)})`; }, limit: LIMITS.memoryBytes / 1e6, limitText: "128 MB" },
			{ label: "Upload size", short: "upload size", sub: "script + Wasm, uncompressed", unit: "MB", digits: 2, get: (k) => mb(q.size(k)?.bytes), limit: LIMITS.uploadBytes / 1e6, limitText: "64 MiB", once: true },
			{ label: "JS↔Wasm crossings", short: "JS↔Wasm crossings", sub: "per warm turn (of them hop notices)", unit: "", get: (k, n) => (T[k].kind === "pi" ? 0 : q.probe("crossings", k, n, { host })), extra: (k, n) => (T[k].kind === "pi" ? "" : ` (${fmt(q.probe("hopSteps", k, n, { host }))})`) },
			...(cfh ? [] : [{ label: "Time inside the core", short: "core time", sub: "per warm turn, Miniflare timer", unit: "ms", digits: 1, get: (k, n) => (T[k].kind === "pi" ? undefined : q.probe("coreWarm", k, n)), info: true, none: "no core" }]),
		];
		// One value cell; the PiG cell is red where it loses and green where it wins. `span` 2 for size-independent rows.
		const cell = (row, k, n, v, cmp, span = 1) => {
			const attrs = (cls) => `${span > 1 ? ` colspan="${span}"` : ""} class="${cls}${span > 1 ? " c" : ""}"`;
			if (v === undefined) return `<td${attrs("muted")}>${T[k].kind === "pi" && row.none ? row.none : "–"}</td>`;
			const over = row.limit !== undefined && v > row.limit;
			const cls = k === g && cmp?.kind === "lose" ? "bad" : k === g && cmp?.kind === "win" ? "good" : "";
			return `<td${attrs(cls)}>${fmt(v, row.digits ?? 0)}${row.unit ? ` <span class="u">${row.unit}</span>` : ""}${row.extra ? `<span class="muted">${esc(row.extra(k, n))}</span>` : ""}${over ? ` <span class="bad">▲</span>` : ""}</td>`;
		};
		const losses = [];
		const body = rows
			.map((row) => {
				const per = (row.once ? [sizes[0]] : sizes).map((n) => {
					const [vg, vp] = [row.get(g, n), row.get(p, n)];
					return { n, vg, vp, cmp: row.info ? { kind: "none" } : verdict(vg, vp) };
				});
				const lost = per.filter((x) => x.cmp.kind === "lose");
				const won = per.filter((x) => x.cmp.kind === "win");
				const at = (x) => (row.once ? "" : ` at ${fmt(x.n)}`);
				const ratio = (x) => `${fmt(x.cmp.ratio, x.cmp.ratio < 10 ? 1 : 0)}×`;
				const parts = [];
				if (lost.length) {
					const finite = lost.filter((x) => Number.isFinite(x.cmp.ratio));
					parts.push(`<span class="bad">we lose: ${finite.length ? finite.map((x) => `${ratio(x)}${at(x)}`).join(", ") : "pi-durable has none"}</span>`);
					losses.push(row.short);
				}
				if (won.length) parts.push(`<span class="good">we win: ${won.map((x) => `${ratio(x)}${at(x)}`).join(", ")}</span>`);
				const slight = per.filter((x) => x.cmp.kind === "slight");
				if (slight.length) parts.push(`<span class="muted">slightly better: ${slight.map((x) => `${fmt(x.cmp.ratio, 2)}×${at(x)}`).join(", ")}</span>`);
				if (!parts.length) {
					const warm = row.info ? q.metric("warm", g, 3500, { host }) : undefined;
					const v = per.at(-1).vg;
					parts.push(row.info ? `<span class="muted">${row.unit === "ms" && v !== undefined && warm ? `${fmt((100 * v) / warm)}% of the warm turn at 3,500` : row.unit === "MB" && v !== undefined ? `${fmt((100 * v) / row.limit)}% of the limit at 3,500` : "–"}</span>` : per.some((x) => x.cmp.kind === "tie") ? `<span class="muted">tie</span>` : `<span class="muted">–</span>`);
				}
				const values = row.once
					? `${cell(row, p, 0, per[0].vp, undefined, 2)}${cell(row, g, 0, per[0].vg, per[0].cmp, 2)}`
					: per.map((x) => `${cell(row, p, x.n, x.vp)}${cell(row, g, x.n, x.vg, x.cmp)}`).join("");
				return `<tr><th><div>${esc(row.label)}</div><div class="sub">${esc(row.sub)}</div></th>${values}<td class="lim">${esc(row.limitText ?? "none")}</td><td class="v">${parts.join("<br>")}</td></tr>`;
			})
			.join("");
		const tcss = `<style>
.where { width: 100%; border-collapse: collapse; font-size: 18px; }
.where td { white-space: nowrap; }
.where th, .where td { padding: 2px 12px; border-bottom: 1px solid ${C.line}; text-align: right; font-variant-numeric: tabular-nums; }
.where thead th { font-size: 14px; white-space: nowrap; color: ${C.muted}; font-weight: 600; border-bottom: 2px solid ${C.line}; }
.where tbody th { text-align: left; font-weight: 700; }
.where .sub { font-size: 12.5px; color: ${C.muted}; font-weight: 400; white-space: nowrap; }
.where .u { font-size: 14px; color: ${C.muted}; }
.where .c { text-align: center; }
.where .lim { color: ${C.muted}; font-size: 16px; }
.where .v { text-align: left; font-size: 16px; font-weight: 600; }
.where .grp { text-align: center; color: ${C.text}; font-size: 16px; }
</style>`;
		const head = `<thead><tr><th></th><th colspan="2" class="grp">50 turns</th><th colspan="2" class="grp">3,500 turns</th><th></th><th></th></tr>
<tr><th style="text-align:left">Lower is better</th>${sizes.map(() => `<th style="color:${C.pi}">pi-durable</th><th style="color:${C.pig}">${esc(T[g].build ?? "PiG")}</th>`).join("")}<th>Cloudflare limit</th><th style="text-align:left">Verdict</th></tr></thead>`;
		const cf = cfh ? ` Measured on Cloudflare: ${(meta.cloudflare?.label ?? "?").replace(/\.$/, "")}.` : (data.probes ?? []).some((x) => x.host === "cloudflare") ? " Measured on Miniflare (local workerd); Cloudflare on the next slide." : " Measured on Miniflare (local workerd); Cloudflare numbers follow when the account is ready.";
		const method = cfh
			? "Latency at the client includes the network; compare the two columns, not other slides. CPU time is Cloudflare's own figure per invocation (wrangler tail): the Worker plus the Durable Object calls it made. Cloudflare does not expose the isolate's JS heap, so memory shows the core's Wasm memory only."
			: "Memory: what the isolate retains after a full collection after the last turn (Wasm memory never shrinks); in brackets the largest reading after a request, before collection, which counts garbage. Cloudflare freezes timers during a request, so core time is Miniflare-only.";
		slides.push({
			name: cfh ? "where-it-runs-cloudflare" : "where-it-runs",
			html: page(
				cfh ? "where-it-runs-cloudflare" : "where-it-runs",
				`${tcss}<h1>Where TinyGo runs${cfh ? ", on Cloudflare" : ": any disadvantage?"}${rule([g, p])}</h1>
		<h2 style="font-size:20px">${esc(name(g))} and pi-durable main in the same Durable Object class, request by request.${losses.length ? ` <span class="bad">We lose on ${esc(losses.join(", "))}.</span>` : " No metric where we lose."}${esc(cf)}</h2>
		<div class="panel" style="padding:12px 22px"><table class="where">${head}<tbody>${body}</tbody></table></div>
		<div class="foot" style="font-size:13.5px;line-height:1.4">▲ above the limit. Cloudflare limits (docs, 2026-10-08): CPU 10 ms per request on Workers Free, 30 s by default on Paid; 128 MB per isolate, JS heap and Wasm included; 64 MiB per Worker, uncompressed, and no compressed limit (gzip: ${[p, g].map((k) => `${esc(T[k].build ?? T[k].label)} ${fmt(mb(q.size(k)?.gzip), 2)} MB`).join(", ")}). A Durable Object is billed by wall-clock duration, requests and SQLite rows, not by CPU time. ${method}</div>`,
			),
		});
	}

	// 6. Cloudflare, only when measured there.
	if (data.samples.some((s) => s.host === "cloudflare")) {
		const sizes = q.sizesOf("standard", "cloudflare");
		const keys = [r.headline, r.reference, r.contender, ...r.detail].filter((k) => q.targetsOf("standard", "cloudflare").includes(k));
		slides.push({
			name: "cloudflare",
			html: page(
				"cloudflare",
				`<h1>On Cloudflare${rule(keys)}</h1>
		<h2>${esc(meta.cloudflare?.label ?? "")}</h2>
		${legend(keys)}
		<div class="row">
			${panel("Warm turn, ms", chart("warm", keys, { sizes, host: "cloudflare" }))}
			${panel("Cold start, ms", chart("cold", keys, { sizes, host: "cloudflare" }))}
		</div>`,
				`Cloudflare, ${esc(meta.cloudflare?.label ?? "?")} Not comparable with the ${esc(machine.host ?? "local")} slides; compare targets on this slide only.`,
			),
		});
	}

	// 7. Method.
	{
		const runner = meta.runner ?? {};
		const proto = meta.protocol ?? {};
		const cf = meta.cloudflare?.status === "measured" ? `Cloudflare numbers: ${esc(meta.cloudflare.label)}.` : "Local workerd (Miniflare) only: no Cloudflare production numbers in this deck.";
		slides.push({
			name: "method",
			html: page(
				"method",
				`<h1>How this was measured</h1>
	<style>.method li { font-size: 16.5px; line-height: 1.3; } .method ul { gap: 6px; }</style>
	<div class="row method"><div class="panel col"><ul>
		<li>The benchmark is Mario Zechner's fork of Tardigrade's durable-bench, <span class="mono">${esc(runner.repo ?? "badlogic/durable-bench")}</span> at <span class="mono">${esc(runner.commit ?? "?")}</span>, scenario unchanged: scripted model, one lookup tool, histories built by real turns (tool, tool, no tool), measured turns with 8 tool calls.${runner.changes ? ` Changes: ${esc(runner.changes)}` : ""}</li>
		<li>Contestants: ${contenders.concat(r.detail, r.control).map((k) => `${esc(name(k))} <span class="mono">${esc(T[k].version)}</span>`).join(", ")}.</li>
		<li>Same model for everyone: ${esc(meta.model?.label ?? "?")}</li>
		<li>${contractOk ? `Same rows: PiG Durable's core <span class="mono">${esc(contract.core)}</span> writes the same rows at the same commit boundaries as pi-durable main <span class="mono">${esc(ref.version)}</span> on every runnable row of the contract corpus${contract.rows ? ` (${fmt(contract.rows.ready - (contract.rows.skipped_by_reference?.length ?? 0))} rows; ${fmt(contract.rows.skipped + (contract.rows.skipped_by_reference?.length ?? 0))} rows the gate skips for every implementation, the reference included, with recorded reasons${contract.rows.pending_owner?.length ? `; ${esc(contract.rows.pending_owner.join(", "))} wait for provider recordings made with the owner's API keys` : ""})` : ""}, and the crash matrix passes${contract.identity?.standard ? ` (at each history size of the standard workload: gate rows ${esc(Object.values(contract.identity.standard).join(", "))})` : ""}. Verification: <span class="mono">${esc(contract.report_url)}</span>` : `<span class="bad">Row identity: pending. The production core has not passed the CONTRACT gate, so no PiG number here is a measurement.</span>`}</li>
		<li>${esc(machine.host ?? "?")}: ${esc(machine.cpu ?? "?")}, ${esc(machine.pinning ?? "?")}. One interleaved session${meta.session?.id ? ` (<span class="mono">${esc(meta.session.id)}</span>)` : ""}: targets alternate within every round, ${esc(proto.samples ?? "?")} fresh runtimes per target and size. ${esc(machine.relative ?? "")}</li>
		<li>Cold = open + first turn in a fresh runtime (runtime startup excluded), one per runtime. Warm = every later turn of every runtime, pooled (${esc(fmt((proto.samples ?? 0) * ((proto.turns ?? 10) - 1)))} per target and size). We report p50, p95 and max, net of the floor: the p50 of an empty target (same Worker, request and Durable Object path, no agent) at the same size. Rule A: durable-bench's JS model on pi-durable's model path, in a Durable Object (every headline number). Rule B: the native build's own scripted model in the core's harness, a harness floor. DB = SQLite file after the turns. History = each system seeds its own history${proto.seed === "chain" ? ", timed as the sum of contiguous seeding segments from an empty store, each segment's end checked by its context fingerprint" : " from an empty store"}. ${cf}</li>
		${meta.text?.method ? `<li>${esc(meta.text.method)}</li>` : ""}
		<li>PiG is a Go implementation of Pi, created by Michael Kinsy and originally developed at Hewlett Packard Enterprise; it is not an official Pi project. Thanks to Mario Zechner and the Tardigrade team for the benchmark.</li>
		<li>Raw results: <span class="mono">${esc(meta.publish?.results_path ?? "bench/durable/report/data/results.jsonl")}</span> (sha256 <span class="mono">${data.sha256.slice(0, 16)}</span>); every slide is regenerated from that one file.</li>
	</ul></div></div>`,
			),
		});
	}
	return slides;
}
