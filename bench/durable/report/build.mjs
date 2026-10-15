#!/usr/bin/env node
// Regenerates every slide from one results file: validate and gate, write HTML, render PNGs, write review tables.
// usage: node build.mjs <results.jsonl> --out <dir> [--html-only]
import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { ResultsError, parse, tables, validate } from "./lib/results.mjs";
import { renderSlides } from "./lib/slides.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const flag = (f) => args.includes(f);
const opt = (f) => (args.includes(f) ? args[args.indexOf(f) + 1] : undefined);
const input = args.find((a, i) => !a.startsWith("--") && args[i - 1] !== "--out");
if (!input || !opt("--out")) {
	console.error("usage: node build.mjs <results.jsonl> --out <dir> [--html-only]");
	process.exit(2);
}
const out = resolve(opt("--out"));

let data;
try {
	data = parse(readFileSync(input, "utf8"));
	for (const w of validate(data)) console.warn(`warning: ${w}`);
	// A deck without the watermark exists only in final/, and final/ only holds a final deck.
	if (data.meta.status !== "final" && basename(out) === "final") throw new ResultsError(`a ${data.meta.status} results file must not render into final/`);
	if (data.meta.status === "final" && basename(out) !== "final") throw new ResultsError("a final results file renders into final/");
} catch (e) {
	if (e instanceof ResultsError) {
		console.error(`refusing to render ${input}: ${e.message}`);
		process.exit(1);
	}
	throw e;
}

const fonts = {
	inter: join(here, "node_modules/@fontsource-variable/inter/files/inter-latin-wght-normal.woff2"),
	mono: join(here, "node_modules/@fontsource-variable/jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2"),
};
const slides = renderSlides(data, { fonts });

for (const d of ["html", "png"]) {
	rmSync(join(out, d), { recursive: true, force: true });
	mkdirSync(join(out, d), { recursive: true });
}
const files = slides.map((s, i) => {
	const file = join(out, "html", `${String(i + 1).padStart(2, "0")}-${s.name}.html`);
	writeFileSync(file, s.html);
	return file;
});
writeFileSync(join(out, "tables.md"), tables(data));
writeFileSync(
	join(out, "SOURCE.txt"),
	`results ${relative(out, resolve(input))}\nsha256 ${data.sha256}\nstatus ${data.meta.status}\nsession ${data.meta.session?.id ?? "-"}\nslides ${slides.map((s) => s.name).join(" ")}\n`,
);
console.log(`${slides.length} slides from ${input} (${data.meta.status}, sha256 ${data.sha256.slice(0, 16)})`);
if (flag("--html-only")) process.exit(0);

const { shoot } = await import("./lib/shoot.mjs");
const shots = await shoot(files, join(out, "png"));
let bad = 0;
for (const s of shots) {
	console.log(`${basename(s.png)} ${s.problems.length ? `\n  ${s.problems.join("\n  ")}` : "ok"}`);
	bad += s.problems.length;
}
if (bad) {
	console.error(`${bad} layout problems`);
	process.exit(1);
}
console.log(`png: ${readdirSync(join(out, "png")).length} files in ${out}/png`);
