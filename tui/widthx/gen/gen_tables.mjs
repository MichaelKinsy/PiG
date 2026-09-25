// Generates tui/widthx/unicode_tables.go from the SAME sources upstream
// pi-tui's graphemeWidth uses at runtime, so PiG's width is Pi's by
// construction:
//   - get-east-asian-width@1.6.0 (upstream packages/tui dependency, pinned)
//   - V8/ICU property escapes (\p{Default_Ignorable_Code_Point}, \p{Mark},
//     \p{RGI_Emoji}, ...) evaluated by this Node runtime
//   - UCD files matching this Node's process.versions.unicode for the
//     grapheme-cluster rules Intl.Segmenter implements (GraphemeBreakProperty,
//     DerivedCoreProperties InCB, emoji-data Extended_Pictographic, and
//     emoji-zwj-sequences for RGI ZWJ candidates, each confirmed by V8).
//
// Usage (from tui/widthx):
//   node gen/gen_tables.mjs <UCD 17.0.0 dir> > unicode_tables.go
// Every input (the UCD files, the vendored get-east-asian-width in
// testdata/pi, and this script) must hash as pinned in gen/inputs.sha256, and
// Node's Unicode version must match; otherwise the script refuses to run. It
// records the manifest digest in the output header, and writes the digest of
// the output it produced to gen/output.sha256. pi_tables_manifest_test.go
// checks all three.
import fs from "node:fs";
import path from "node:path";

import crypto from "node:crypto";
import { fileURLToPath } from "node:url";

const ucd = process.argv[2];
const here = path.dirname(fileURLToPath(import.meta.url));
const widthx = path.dirname(here);
const eawDir = path.join(widthx, "testdata", "pi", "get-east-asian-width");
const sha256 = (buf) => crypto.createHash("sha256").update(buf).digest("hex");
const manifestPath = path.join(here, "inputs.sha256");
const manifest = fs.readFileSync(manifestPath);
const roots = { ucd, eaw: eawDir, gen: here };
for (const line of manifest.toString("utf8").split("\n")) {
	const l = line.replace(/#.*/, "").trim();
	if (!l) continue;
	const [key, value, ...rest] = l.split(/\s+/);
	if (key === "node-unicode") {
		if (process.versions.unicode !== value) throw new Error(`node Unicode ${process.versions.unicode}, manifest pins ${value}`);
		continue;
	}
	const [root, rel] = value.split(":");
	const file = path.join(roots[root], rel);
	const got = sha256(fs.readFileSync(file));
	if (got !== key) throw new Error(`${value}: sha256 ${got}, manifest pins ${key}`);
}
const manifestDigest = sha256(manifest);
const eaw = (await import(path.join(eawDir, "index.js"))).eastAsianWidth;
const eawVersion = JSON.parse(fs.readFileSync(path.join(eawDir, "package.json"), "utf8")).version;

// Exactly upstream packages/tui/src/utils.ts classes, decomposed per code point.
const reZW = /^(?:\p{Default_Ignorable_Code_Point}|\p{Control}|\p{Mark}|\p{Surrogate})$/v;
const reNP = /^(?:\p{Default_Ignorable_Code_Point}|\p{Control}|\p{Format}|\p{Mark}|\p{Surrogate})$/v;
const reMark = /^\p{Mark}$/v;
const reTSM = /^(?:[\p{Spacing_Mark}--[᜴〮〯]]|[ٟཿါာေဳ-ဵး်-ှ])$/v;
const reRGI = /^\p{RGI_Emoji}$/v;
const reEMB = /^\p{Emoji_Modifier_Base}$/v;
// upstream cjkBreakRegex (utils.ts) and JavaScript's \s / String.prototype.trim set.
const reCJK = /[\p{Script_Extensions=Han}\p{Script_Extensions=Hiragana}\p{Script_Extensions=Katakana}\p{Script_Extensions=Hangul}\p{Script_Extensions=Bopomofo}]/u;
const reJSSpace = /^\s$/u;

const GCB = { Other: 0, CR: 1, LF: 2, Control: 3, Extend: 4, ZWJ: 5, Regional_Indicator: 6, Prepend: 7, SpacingMark: 8, L: 9, V: 10, T: 11, LV: 12, LVT: 13 };
const gcb = new Uint8Array(0x110000);
const incb = new Uint8Array(0x110000); // 1 Consonant, 2 Extend, 3 Linker
const extpict = new Uint8Array(0x110000);
function parse(file, fn) {
	for (const line of fs.readFileSync(path.join(ucd, file), "utf8").split("\n")) {
		const l = line.replace(/#.*/, "").trim();
		if (!l) continue;
		const f = l.split(";").map((s) => s.trim());
		const [a, b] = f[0].split("..").map((h) => parseInt(h, 16));
		for (let cp = a; cp <= (b ?? a); cp++) fn(cp, f);
	}
}
parse("auxiliary/GraphemeBreakProperty.txt", (cp, f) => { gcb[cp] = GCB[f[1]]; });
parse("DerivedCoreProperties.txt", (cp, f) => {
	if (f[1] === "InCB") incb[cp] = { Consonant: 1, Extend: 2, Linker: 3 }[f[2]];
});
parse("emoji/emoji-data.txt", (cp, f) => { if (f[1] === "Extended_Pictographic") extpict[cp] = 1; });

// Property bits.
const B = { W2: 1, ZW: 2, NP: 4, MARK: 8, TSM: 16, RGI1: 32, EXTPICT: 64, CJK: 128, JSSPACE: 1 << 14 };
const props = new Uint32Array(0x110000);
const rgi = new Set();
for (let cp = 0; cp < 0x110000; cp++) {
	const s = String.fromCodePoint(cp);
	let p = 0;
	if (eaw(cp) === 2) p |= B.W2;
	if (reZW.test(s)) p |= B.ZW;
	if (reNP.test(s)) p |= B.NP;
	if (reMark.test(s)) p |= B.MARK;
	if (reTSM.test(s)) p |= B.TSM;
	if (reRGI.test(s)) p |= B.RGI1;
	if (extpict[cp]) p |= B.EXTPICT;
	if (reCJK.test(s)) p |= B.CJK;
	if (reJSSpace.test(s)) p |= B.JSSPACE;
	if (cp >= 0xd800 && cp <= 0xdfff) p = B.ZW | B.NP; // unreachable from valid UTF-8
	p |= gcb[cp] << 8;
	p |= incb[cp] << 12;
	props[cp] = p;
	const vs = s + "️";
	if (reRGI.test(vs)) rgi.add(vs);
	if (reEMB.test(s)) for (let m = 0x1f3fb; m <= 0x1f3ff; m++) { const t = s + String.fromCodePoint(m); if (reRGI.test(t)) rgi.add(t); }
}
for (const k of "0123456789#*") { const t = k + "️⃣"; if (reRGI.test(t)) rgi.add(t); }
for (let a = 0x1f1e6; a <= 0x1f1ff; a++) for (let b = 0x1f1e6; b <= 0x1f1ff; b++) {
	const t = String.fromCodePoint(a, b); if (reRGI.test(t)) rgi.add(t);
}
const tag = (s) => [...s].map((c) => String.fromCodePoint(0xe0000 + c.charCodeAt(0))).join("");
for (const sub of ["gbeng", "gbsct", "gbwls", "usca", "ustx", "gbnir"]) {
	const t = "\u{1F3F4}" + tag(sub) + "\u{E007F}"; if (reRGI.test(t)) rgi.add(t);
}
let zwjFile = 0, zwjRejected = 0;
parse("emoji/emoji-zwj-sequences.txt", () => {}); // syntax check
for (const line of fs.readFileSync(path.join(ucd, "emoji/emoji-zwj-sequences.txt"), "utf8").split("\n")) {
	const l = line.replace(/#.*/, "").trim();
	if (!l) continue;
	const t = String.fromCodePoint(...l.split(";")[0].trim().split(/\s+/).map((h) => parseInt(h, 16)));
	zwjFile++;
	if (reRGI.test(t)) rgi.add(t); else zwjRejected++;
}

// Emit ranges.
const out = [];
out.push("// Code generated by gen/gen_tables.mjs; DO NOT EDIT.");
out.push(`// Node ${process.version}, Unicode ${process.versions.unicode}, ICU ${process.versions.icu}, get-east-asian-width ${eawVersion}.`);
out.push(`// Inputs: gen/inputs.sha256 sha256 ${manifestDigest}.`);
out.push(`// RGI multi-code-point sequences: ${rgi.size} (ZWJ file lines ${zwjFile}, rejected by V8 ${zwjRejected}).`);
out.push("");
out.push("package widthx");
out.push("");
out.push(`const unicodeTablesVersion = "unicode ${process.versions.unicode} / get-east-asian-width ${eawVersion}"`);
out.push("");
out.push("// propRanges: [lo, hi, props] triples, sorted, covering 0..0x10FFFF.");
out.push("var propRanges = [...]uint32{");
let lo = 0;
let row = [];
for (let cp = 1; cp <= 0x110000; cp++) {
	if (cp === 0x110000 || props[cp] !== props[lo]) {
		row.push(`0x${lo.toString(16)}, 0x${(cp - 1).toString(16)}, 0x${props[lo].toString(16)},`);
		if (row.length === 4) { out.push("\t" + row.join(" ")); row = []; }
		lo = cp;
	}
}
if (row.length) out.push("\t" + row.join(" "));
out.push("}");
out.push("");
out.push("// rgiSequences: multi-code-point strings matching /^\\p{RGI_Emoji}$/v.");
out.push("var rgiSequences = [...]string{");
for (const s of [...rgi].sort()) {
	out.push("\t\"" + [...s].map((c) => { const cp = c.codePointAt(0); return cp < 0x10000 ? "\\u" + cp.toString(16).padStart(4, "0") : "\\U" + cp.toString(16).padStart(8, "0"); }).join("") + "\",");
}
out.push("}");
const text = out.join("\n") + "\n";
fs.writeFileSync(path.join(here, "output.sha256"), `${sha256(text)}  unicode_tables.go\n`);
process.stdout.write(text);
