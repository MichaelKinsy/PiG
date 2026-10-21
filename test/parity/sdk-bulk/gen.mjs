// Generates the pi-tui helper vectors every extension SDK (Go, Python, Rust) must reproduce byte for byte.
//
// The expected values come from the pinned Pi package itself: this script imports the pi-tui dist of the Pi
// release and records what its own functions return. Nothing here is computed by PiG code.
//
// Usage: node gen.mjs <pi-tui package dir> <get-east-asian-width dir> <out dir>
//   e.g. (from test/parity/sdk-bulk) node gen.mjs <node_modules>/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui ../../../tui/widthx/testdata/pi/get-east-asian-width .
// The pi-tui package must be the one the upstream pin names (.upstream/pin package_version).
//
// Output files (JSON, committed): tui-text.json, tui-keys.json, tui-fuzzy.json, tui-colors.json, tui-terminal.json.
// Determinism: a fixed-seed PRNG; rerunning against the same pin reproduces the files exactly.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const [srcDir, eawDir, outDir] = process.argv.slice(2);
if (!srcDir || !eawDir || !outDir) throw new Error("usage: gen.mjs <tui/src> <get-east-asian-width> <out>");

// Pi's utils.ts imports a bare "get-east-asian-width"; stage the sources next to a node_modules holding it.
const stage = fs.mkdtempSync(path.join(os.tmpdir(), "pig-vectors-"));
fs.cpSync(path.join(srcDir, "dist"), path.join(stage, "src"), { recursive: true });
fs.writeFileSync(path.join(stage, "package.json"), '{"type":"module"}');
const piVersion = JSON.parse(fs.readFileSync(path.join(srcDir, "package.json"), "utf8")).version;
fs.mkdirSync(path.join(stage, "node_modules"));
fs.symlinkSync(path.resolve(eawDir), path.join(stage, "node_modules", "get-east-asian-width"));
const load = (name) => import(pathToFileURL(path.join(stage, "src", name.replace(/\.ts$/, ".js"))).href);
const utils = await load("utils.ts");
const keys = await load("keys.ts");
const fuzzy = await load("fuzzy.ts");
const colors = await load("colors.ts");
const keybindings = await load("keybindings.ts");
const image = await load("terminal-image.ts");
// CURSOR_MARKER lives in tui.ts, which pulls in the whole terminal stack; read its literal from the pinned source.
const cursorMarker = new Function(`return ${/export const CURSOR_MARKER = ("[^"]*");/.exec(fs.readFileSync(path.join(stage, "src", "tui.js"), "utf8"))[1]}`)();
const meta = { pi: piVersion, node: process.version, unicode: process.versions.unicode };

let seed = 0x9e3779b9;
const rnd = () => {
	seed ^= seed << 13; seed >>>= 0;
	seed ^= seed >>> 17;
	seed ^= seed << 5; seed >>>= 0;
	return seed / 4294967296;
};
const pick = (a) => a[Math.floor(rnd() * a.length)];
const int = (lo, hi) => lo + Math.floor(rnd() * (hi - lo + 1));

const write = (name, value) => fs.writeFileSync(path.join(outDir, name), JSON.stringify({ ...meta, ...value }) + "\n");

// ---------------------------------------------------------------- text
const plain = ["a", "hello", "world", "foo-bar", "x", "The quick brown fox", "  ", " ", "a b", "path/to/file.ts", "12345", "~/src", "|", "—", "…", "é", "ß", "İ"];
const wide = ["漢字", "かな", "カナ", "한국어", "ＡＢＣ", "。", "、", "「", "」", "你好，世界", "日本語のテキスト", "각", "ᄀ가"];
const emoji = ["😀", "👍🏽", "👨‍👩‍👧‍👦", "🏳️‍🌈", "🇺🇸", "🇬🇧🇫🇷", "❤️", "❤", "⚠️", "⚠", "✓", "✔", "⭐", "⌚", "1️⃣", "#️⃣", "🧑🏾‍🦰", "🫱🏻‍🫲🏿", "☺️", "©️", "↔️", "⬜️", "🏴‍☠️", "🔥", "🤝", "👩‍💻"];
const marks = ["e\u0301", "a\u0308", "Z\u0334\u0322\u031b\u0317\u0353", "क्ष", "क्‍ष", "स्त्र", "ক্ষ", "ન્ન", "ำ", "กำ", "ຳ", "ລຳ", "\u0e31", "\u200d", "\u200b", "\u200c", "\ufeff", "\u2060", "\u00ad", "\u0300", "👍\u{1f3fd}"];
const ctrl = ["\x00", "\x07", "\x7f", "\x85", "\t", "\x08", "\x0b", "\x0c", "\x01", "\x1f", "\u2028", "\u2029", "\xa0", "\u3000", "\u2003"];
const ansi = [
	"\x1b[31m", "\x1b[0m", "\x1b[m", "\x1b[1;38;5;196m", "\x1b[48;2;1;2;3m", "\x1b[39m", "\x1b[22m", "\x1b[4m", "\x1b[24m", "\x1b[49m",
	"\x1b[7m", "\x1b[27m", "\x1b[9m", "\x1b[29m", "\x1b[3m", "\x1b[23m", "\x1b[2m", "\x1b[1m", "\x1b[38;2;10;20;30m", "\x1b[4:3m", "\x1b[58;5;1m",
	"\x1b[2K", "\x1b[H", "\x1b[?25l", "\x1b[200~", "\x1b[1;5A", "\x1b[5n", "\x1b[38:2::1:2:3m", "\x1b[999999999m",
	"\x1b]8;;http://x.test/a\x07", "\x1b]8;;\x07", "\x1b]8;id=1;https://e.com/p?q=1\x1b\\", "\x1b]8;;\x1b\\", "\x1b]0;title\x07", "\x1b]133;A\x07", "\x1b]133;B\x1b\\",
	"\x1b_pi:c\x07", "\x1b_Gf=100,a=T;AAAA\x1b\\", "\x1b]1337;File=inline=1:AAAA\x07", "\x1bP1$r\x1b\\", "\x1b^pm\x1b\\", "\x1bX sos\x1b\\",
	"\x1b", "\x1b[", "\x1b]8;;unterminated", "\x1b[31", "\x1bc", "\x1b7", "\x1b8", "\x1b(B", "\x1b=", "\x1b>",
];
const atoms = [...plain, ...plain, ...plain, ...wide, ...emoji, ...marks, ...ctrl, ...ansi, ...ansi];
const wrapAtoms = [...plain, ...plain, ...plain, ...plain, " ", " ", " ", " ", "\n", "\n", ...wide, ...emoji, ...marks, ...ansi, "\t", "  ", "-", "/"];
const gen = (pool, min, max) => {
	let s = "";
	for (let n = int(min, max); n > 0; n--) s += pick(pool);
	return s;
};
const dedupe = (a) => [...new Set(a)];

const fixed = [
	"", " ", "a", "abc", "hello world", "\t", "a\tb", "\n", "a\nb", "\r", "a\r\nb", "\x1b[31mred\x1b[0m", "\x1b[31mred", "x\x1b[0m",
	"\x1b]8;;http://a\x07link\x1b]8;;\x07", "\x1b]8;;http://a\x1b\\link\x1b]8;;\x1b\\ tail", "漢字漢字", "😀😀😀", "👨‍👩‍👧‍👦👨‍👩‍👧‍👦", "🇺🇸🇬🇧🇫🇷",
	"e\u0301e\u0301", "a\u200db", "\u200b", "\u200d", "ＡＢ", "ｱｲｳ", "\x1b_pi:c\x07x", "x\x1b_pi:c\x07", "\x1b[38;2;1;2;3mA\x1b[39m",
	"\x1b[1m\x1b[4mbold under\x1b[24m still bold\x1b[22m", "\x1b[48;5;236m bg \x1b[0m", "AAAAAAAAAA BBBBBBBBBB CCCCCCCCCC",
	"averyveryveryverylongwordwithoutanyspaces", "日本語の長い文章がここにあります。折り返しのテスト、です。", "a  b   c    d", "   leading", "trailing   ",
	"https://example.com/a/very/long/path/that/never/breaks/anywhere/at/all?query=1#frag", "\x1b]8;;https://example.com/long\x07https://example.com/long\x1b]8;;\x07",
];
const textCorpus = dedupe([...fixed, ...Array.from({ length: 2600 }, () => gen(atoms, 1, 14))]);
const wrapCorpus = dedupe([...fixed, ...Array.from({ length: 1500 }, () => gen(wrapAtoms, 1, 40))]);

const out = { visibleWidth: [], strip: [], truncate: [], wrap: [], slice: [], segments: [], link: [], bg: [], cursorMarker };
for (const s of textCorpus) {
	out.visibleWidth.push([s, utils.visibleWidth(s)]);
	out.strip.push([s, utils.stripTerminalSequences(s)]);
	out.bg.push([s, utils.getActiveBackgroundAnsi(s)]);
}
const ellipses = ["…", "...", "", "→", " …", "\x1b[2m…\x1b[22m", "漢", "😀"];
for (let i = 0; i < 4200; i++) {
	const s = i < textCorpus.length ? textCorpus[i] : pick(textCorpus);
	const w = int(0, 30), e = rnd() < 0.5 ? "…" : pick(ellipses), pad = rnd() < 0.4;
	const args = [s, w, e, pad];
	const useDefaults = rnd() < 0.1;
	out.truncate.push([s, w, useDefaults ? null : e, useDefaults ? null : pad, useDefaults ? utils.truncateToWidth(s, w) : utils.truncateToWidth(s, w, e, pad)]);
	void args;
}
for (let i = 0; i < 3000; i++) {
	const s = i < wrapCorpus.length ? wrapCorpus[i] : pick(wrapCorpus);
	const w = int(1, 50);
	out.wrap.push([s, w, utils.wrapTextWithAnsi(s, w)]);
}
for (let i = 0; i < 3000; i++) {
	const s = pick(textCorpus), start = int(0, 25), len = int(0, 25), strict = rnd() < 0.5;
	out.slice.push([s, start, len, strict, utils.sliceByColumn(s, start, len, strict), utils.sliceWithWidth(s, start, len, strict)]);
}
for (let i = 0; i < 1500; i++) {
	const s = pick(textCorpus), be = int(0, 15), as = be + int(0, 10), al = int(0, 20), strict = rnd() < 0.5;
	out.segments.push([s, be, as, al, strict, utils.extractSegments(s, be, as, al, strict)]);
}
for (const s of textCorpus.filter((x) => x.includes("\x1b]8;")).concat(fixed)) {
	const w = utils.visibleWidth(s);
	for (let col = 0; col <= Math.min(w + 1, 12); col++) out.link.push([s, col, utils.getOsc8LinkAtColumn(s, col) ?? null]);
}
write("tui-text.json", out);

// ---------------------------------------------------------------- keys
const mods = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 16, 17, 33, 65, 129, 64, 128];
const kdata = new Set();
for (let c = 0; c < 128; c++) kdata.add(String.fromCharCode(c));
for (let c = 1; c < 128; c++) kdata.add("\x1b" + String.fromCharCode(c));
for (const c of ["\x1b\x1b", "\x1b\x1c", "\x1b\x1d", "\x1b\x1f", "\x1bOM", "\x1b[Z", "\x1b\r", "\x1b\n", "\x1b ", "\x1b\x7f", "\x1b\b", "\x1bB", "\x1bF", "\x1bb", "\x1bf", "\x1bp", "\x1bn", "\x00", "é", "ñ", "漢", "😀", "\x1b[200~90:62:3F:A5\x1b[201~", "\x1b[200~\x1b[97;1:3u\x1b[201~"]) kdata.add(c);
const finals = ["A", "B", "C", "D", "H", "F", "E", "P", "Q", "R", "S", "Z", "a", "b", "c", "d", "e"];
for (const f of finals) {
	kdata.add(`\x1b[${f}`); kdata.add(`\x1bO${f}`); kdata.add(`\x1b[[${f}`);
	for (const m of mods) { kdata.add(`\x1b[1;${m + 1}${f}`); for (const ev of [1, 2, 3]) kdata.add(`\x1b[1;${m + 1}:${ev}${f}`); }
}
for (const n of [1, 2, 3, 4, 5, 6, 7, 8, 11, 12, 13, 14, 15, 17, 18, 19, 20, 21, 23, 24, 29, 57427]) {
	for (const t of ["~", "$", "^", "@"]) {
		kdata.add(`\x1b[${n}${t}`); kdata.add(`\x1b[[${n}${t}`);
		for (const m of [0, 1, 2, 4, 5, 8]) { kdata.add(`\x1b[${n};${m + 1}${t}`); for (const ev of [1, 2, 3]) kdata.add(`\x1b[${n};${m + 1}:${ev}${t}`); }
	}
}
const cps = [...Array(26)].map((_, i) => 97 + i).concat([...Array(10)].map((_, i) => 48 + i), [32, 9, 13, 27, 127, 8, 0, 31, 64, 65, 90, 96, 123, 126, 33, 35, 38, 42, 43, 44, 45, 46, 47, 58, 59, 60, 61, 62, 63, 91, 92, 93, 94, 95, 39, 34, 36, 37, 40, 41, 124, 233, 1073, 1103, 0x4e2d, 0x1f600, 57344, 57348, 57358, 57399, 57414, 57417, 57425, 57426, 57427, 57441, 0x110000]);
for (const cp of cps) {
	for (const m of mods) {
		kdata.add(`\x1b[${cp};${m + 1}u`);
		kdata.add(`\x1b[27;${m + 1};${cp}~`);
		if (m % 3 === 0) for (const ev of [1, 2, 3, 4]) kdata.add(`\x1b[${cp};${m + 1}:${ev}u`);
	}
	kdata.add(`\x1b[${cp}u`);
	kdata.add(`\x1b[${cp}::${cp + 1}u`);
	for (const sh of [65, 97, 33, 0, 1103, ""]) {
		kdata.add(`\x1b[${cp}:${sh};2u`); kdata.add(`\x1b[${cp}:${sh}:99;2u`); kdata.add(`\x1b[${cp}:${sh}:1076;6u`); kdata.add(`\x1b[${cp}:${sh}:99;1:3u`); kdata.add(`\x1b[${cp}:${sh};65u`);
	}
	kdata.add(`\x1b[${cp}::99;5u`); kdata.add(`\x1b[${cp}:97:98;2:2u`);
}
for (const bad of ["\x1b[u", "\x1b[;5u", "\x1b[97;u", "\x1b[97;1;2u", "\x1b[97:::u", "\x1b[a;5u", "\x1b[27;5;~", "\x1b[27;;97~", "\x1b[27;5;97;1~", "\x1b[99999999999999999999u", "\x1b[27;2;99999999999~", "\x1b[1;1:3A\x1b[1;1:3A", "x\x1b[97u", "\x1b[97ux"]) kdata.add(bad);
const base = ["escape", "esc", "enter", "return", "tab", "space", "backspace", "delete", "insert", "clear", "home", "end", "pageUp", "pageDown", "up", "down", "left", "right",
	..."abcdefghijklmnopqrstuvwxyz0123456789", "`", "-", "=", "[", "]", "\\", ";", "'", ",", ".", "/", "!", "@", "#", "$", "%", "^", "&", "*", "(", ")", "_", "+", "|", "~", "{", "}", ":", "<", ">", "?", '"',
	...[...Array(12)].map((_, i) => `f${i + 1}`)];
const modNames = ["ctrl", "shift", "alt", "super"];
const prefixes = [""];
for (let mask = 1; mask < 16; mask++) {
	const ms = modNames.filter((_, i) => mask & (1 << i));
	prefixes.push(ms.join("+") + "+");
	if (ms.length > 1) prefixes.push([...ms].reverse().join("+") + "+");
}
const universe = [];
for (const b of base) for (const p of prefixes) universe.push(p + b);
universe.push("ctrl+shift+alt+super+a");
const data = [...kdata];
const env = (map) => { for (const k of ["WT_SESSION", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"]) delete process.env[k]; Object.assign(process.env, map); };
const kbModes = [
	{ kitty: false, env: {} },
	{ kitty: true, env: {} },
	{ kitty: false, env: { WT_SESSION: "abc" } },
	{ kitty: false, env: { WT_SESSION: "abc", SSH_TTY: "/dev/pts/1" } },
];
const keyOut = { universe, data, keyHelper: {}, modes: [] };
// Key helper: record every property and the output of each modifier function over a few keys.
for (const [k, v] of Object.entries(keys.Key)) keyOut.keyHelper[k] = typeof v === "function" ? ["a", "Z", "enter", "f5", "[", "space"].map((x) => v(x)) : v;
for (const mode of kbModes) {
	env(mode.env);
	keys.setKittyProtocolActive(mode.kitty);
	const rows = data.map((d) => {
		const matched = [];
		for (let i = 0; i < universe.length; i++) if (keys.matchesKey(d, universe[i])) matched.push(i);
		return [keys.parseKey(d) ?? null, keys.isKeyRelease(d), keys.isKeyRepeat(d), keys.decodeKittyPrintable(d) ?? null, keys.decodePrintableKey(d) ?? null, matched];
	});
	keyOut.modes.push({ kitty: mode.kitty, env: mode.env, rows });
}
env({});
keys.setKittyProtocolActive(false);
write("tui-keys.json", keyOut);

// ---------------------------------------------------------------- fuzzy
const words = ["foo", "bar", "baz", "Foo", "file.ts", "src/main.go", "my-file_name", "a1", "1a", "abc123", "123abc", "hello world", "HELLO", "İstanbul", "ΣΑΣ", "ΑΣ", "σας", "ǅ", "ß", "ﬁle", "漢字", "😀 smile", "x", "", " ", "/", "a/b/c", "a b", "docs/README.md", "node_modules/.bin", "Résumé", "résumé", "k\u212a", "ſ", "tab\tsep"];
const fz = { match: [], filter: [] };
const queries = ["", " ", "f", "fo", "foo", "FOO", "bar", "a1", "1a", "abc", "123", "abc123", "123abc", "ab12", "12ab", "a b", "a/b", "/", "src main", "main go", "m.go", "ΣΑΣ", "σας", "i", "istanbul", "ss", "fi", "漢", "😀", "re", "k", "s", "he wo", "  foo  ", "foo bar", "x/y", "\t"];
for (const q of queries) for (const t of words) { const r = fuzzy.fuzzyMatch(q, t); fz.match.push([q, t, r.matches, r.score]); }
for (let i = 0; i < 1500; i++) {
	const q = gen([..."abcxyz12 /-_.", "foo", "bar", "Ab", "漢", "é"], 0, 6), t = gen([..."abcdxyz12 /-_.", "foo", "bar", "Ab", "漢", "é"], 0, 14);
	const r = fuzzy.fuzzyMatch(q, t); fz.match.push([q, t, r.matches, r.score]);
}
for (let i = 0; i < 600; i++) {
	const items = Array.from({ length: int(0, 9) }, () => pick(words) + (rnd() < 0.5 ? pick(words) : ""));
	const q = rnd() < 0.5 ? pick(queries) : gen([..."abcfoo12 /"], 0, 5) + (rnd() < 0.3 ? " " + pick(queries) : "");
	fz.filter.push([items, q, fuzzy.fuzzyFilter(items, q, (x) => x)]);
}
write("tui-fuzzy.json", fz);

// ---------------------------------------------------------------- colors
const co = { ops: [] };
const call = (op, args, fn) => {
	try { co.ops.push([op, args, { ok: fn() }]); } catch (e) { co.ops.push([op, args, { error: String(e.message) }]); }
};
const colorInputs = ["#fff", "#FFF", "#000", "#a1b2c3", "#A1B2C3", "#abcd", "#12345", "red", "", "#gggggg", "oklch(70% 0.1 200)", "oklch(0.5 0.2 30deg)", "OKLCH(100% 0.3 150)", "oklch(0% 0 0)", "oklch( 50%  0.1  -10 )",
	"oklch(1.5 0.1 10)", "oklch(0.5 -0.1 10)", "oklch(0.5 1e-1 10)", "oklch(.5 .1 360)", "okhsl(200 50% 50%)", "okhsl(200deg 0.5 0.5)", "okhsl(0 0% 100%)", "okhsl(120 100% 50%)", "okhsl(-30 80% 20%)", "okhsl(10 150% 50%)", "okhsl(10 50% 150%)", "okhsl(10 50 50)",
	0, 1, 7, 15, 16, 21, 46, 123, 196, 231, 232, 244, 255, 256, -1, 1.5, NaN];
for (const v of colorInputs) call("parseColor", [Number.isNaN(v) ? "NaN" : v], () => colors.parseColor(v));
const palette = [];
for (const v of colorInputs) { try { palette.push(colors.parseColor(v)); } catch {} }
for (let i = 0; i < 60; i++) palette.push(colors.rgbColor(int(0, 255), int(0, 255), int(0, 255)));
for (let i = 0; i < 40; i++) palette.push(colors.rgbColor(rnd() * 255, rnd() * 255, rnd() * 255));
for (let i = 0; i < 40; i++) palette.push(colors.oklchColor(rnd(), rnd() * 0.4, rnd() * 720 - 180));
for (let i = 0; i < 20; i++) palette.push(colors.indexedColor(int(0, 255)));
for (const [r, g, b] of [[0, 0, 0], [255, 255, 255], [128, 128, 128], [8, 8, 8], [10, 10, 10], [95, 95, 95], [135, 135, 135], [238, 238, 238], [1, 2, 3], [255, 0, 0], [0, 255, 0], [0, 0, 255]]) palette.push(colors.rgbColor(r, g, b));
for (const c of palette) {
	call("colorToRgb", [c], () => colors.colorToRgb(c));
	call("colorToHex", [c], () => colors.colorToHex(c));
	call("colorToOklch", [c], () => colors.colorToOklch(c));
	call("colorToOkhsl", [c], () => colors.colorToOkhsl(c));
	for (const mode of ["truecolor", "256color"]) {
		call("foregroundAnsi", [c, mode], () => colors.foregroundAnsi(c, mode));
		call("backgroundAnsi", [c, mode], () => colors.backgroundAnsi(c, mode));
	}
}
for (const args of [[0, 0, 0], [255, 255, 255], [1.5, 2, 3], [-1, 0, 0], [256, 0, 0], [0, 0, NaN], [Infinity, 0, 0]]) call("rgbColor", args, () => colors.rgbColor(...args));
for (const args of [[0.5, 0.1, 30], [0.5, 0.1, -30], [0.5, 0.1, 720], [1.1, 0, 0], [-0.1, 0, 0], [0.5, -1, 0], [0.5, NaN, 0]]) call("oklchColor", args, () => colors.oklchColor(...args));
for (const args of [[0, 0, 0], [200, 0.5, 0.5], [-30, 1, 1], [400, 0.2, 0.9], [10, 1.1, 0.5], [10, 0.5, 1.1]]) call("okhslColor", args, () => colors.okhslColor(...args));
for (const i of [0, 1, 255, 256, -1, 1.5, 3]) call("indexedColor", [i], () => colors.indexedColor(i));
for (let i = 0; i < 400; i++) {
	const a = pick(palette), b = pick(palette), amt = pick([0, 0.25, 0.5, 1, rnd(), 1.5, -0.1]), space = pick(["oklch", "srgb", undefined]);
	call("mixColors", [a, b, amt, space ?? null], () => (space === undefined ? colors.mixColors(a, b, amt) : colors.mixColors(a, b, amt, space)));
}
const attrSets = [{}, { bold: true }, { dim: true }, { bold: true, dim: true }, { italic: true }, { underline: true }, { inverse: true }, { strikethrough: true }, { bold: true, italic: true, underline: true, inverse: true, strikethrough: true, dim: true }];
for (let i = 0; i < 300; i++) {
	const attrs = pick(attrSets), fg = rnd() < 0.6 ? pick(palette) : undefined, bg = rnd() < 0.4 ? pick(palette) : undefined, mode = pick(["truecolor", "256color"]), text = pick(["", "x", "hello", "a\x1b[0mb"]);
	const style = { ...attrs, ...(fg ? { fg } : {}), ...(bg ? { bg } : {}) };
	call("styleText", [text, style, mode], () => colors.styleText(text, style, mode));
	const fa = rnd() < 0.7 ? "\x1b[38;5;1m" : undefined, ba = rnd() < 0.5 ? "\x1b[48;5;2m" : undefined;
	call("styleTextWithAnsi", [text, fa ?? null, ba ?? null, attrs], () => colors.styleTextWithAnsi(text, fa, ba, attrs));
}
write("tui-colors.json", co);

// ---------------------------------------------------------------- terminal capabilities, hyperlink
const envKeys = ["TMUX", "TERM", "TERM_PROGRAM", "TERMINAL_EMULATOR", "COLORTERM", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "WARP_SESSION_ID", "WARP_TERMINAL_SESSION_UUID", "ITERM_SESSION_ID", "WT_SESSION", "PI_HYPERLINKS", "PI_IMAGE_PROTOCOL", "PI_TRUE_COLOR"];
const envPool = {
	TMUX: ["/tmp/tmux-1/default,1,0"], TERM: ["xterm-256color", "tmux-256color", "screen", "screen-256color", "xterm-ghostty", "xterm-direct", "alacritty", "dumb", "XTERM-DIRECT", "TMUX", "Screen"],
	TERM_PROGRAM: ["kitty", "ghostty", "WezTerm", "wezterm", "WarpTerminal", "iTerm.app", "alacritty", "vscode", "zed", "Apple_Terminal", "tmux"], TERMINAL_EMULATOR: ["JetBrains-JediTerm", "other"],
	COLORTERM: ["truecolor", "24bit", "TrueColor", "yes", ""], KITTY_WINDOW_ID: ["1"], GHOSTTY_RESOURCES_DIR: ["/x"], WEZTERM_PANE: ["0"], WARP_SESSION_ID: ["s"], WARP_TERMINAL_SESSION_UUID: ["u"], ITERM_SESSION_ID: ["w0t0p0"], WT_SESSION: ["g"],
	PI_HYPERLINKS: ["1", "0", "true", ""], PI_IMAGE_PROTOCOL: ["kitty", "KITTY", "iterm2", "ITerm2", "none", "0", "sixel", ""], PI_TRUE_COLOR: ["1", "0", "yes", ""],
};
const platformDesc = Object.getOwnPropertyDescriptor(process, "platform");
const caps = [];
for (let i = 0; i < 1200; i++) {
	const e = {};
	for (const k of envKeys) if (rnd() < (i < 100 ? 0.1 : 0.22)) e[k] = pick(envPool[k]);
	const platform = rnd() < 0.2 ? "win32" : "linux", tmux = rnd() < 0.5;
	for (const k of envKeys) delete process.env[k];
	Object.assign(process.env, e);
	Object.defineProperty(process, "platform", { value: platform });
	const base = image.detectCapabilities(() => tmux);
	let probes = 0;
	image.detectCapabilities(() => { probes++; return tmux; });
	caps.push({ env: e, platform, tmux, detected: base, tmuxProbed: probes > 0 });
}
Object.defineProperty(process, "platform", platformDesc);
for (const k of envKeys) delete process.env[k];
const links = [["", ""], ["text", "http://x"], ["a\x1b[1mb", "https://e.com/p?q=1&r=2"], ["漢", "file:///tmp/a b"], ["x", ""], ["", "u"]];
write("tui-terminal.json", { capabilities: caps, hyperlink: links.map(([t, u]) => [t, u, image.hyperlink(t, u)]), cursorMarker });
fs.rmSync(stage, { recursive: true, force: true });

// ---------------------------------------------------------------- keybindings
{
	const defs = keybindings.TUI_KEYBINDINGS;
	const ids = Object.keys(defs);
	const custom = { "app.test.a": { defaultKeys: ["ctrl+t", "ctrl+t", "alt+x"], description: "A" }, "app.test.b": { defaultKeys: "ctrl+t" }, "app.test.c": { defaultKeys: [] }, "app.test.d": { defaultKeys: ["f5", "shift+f5"], description: "D" } };
	const merged = { ...defs, ...custom };
	const userSets = [
		{},
		{ "tui.editor.cursorLeft": "ctrl+h" },
		{ "tui.editor.cursorLeft": ["h", "ctrl+h", "h"], "tui.editor.cursorRight": [] },
		{ "tui.editor.cursorLeft": "ctrl+x", "tui.editor.cursorRight": "ctrl+x", "tui.select.up": ["ctrl+x", "k"] },
		{ "app.test.a": "enter", "app.test.c": ["escape"], "app.test.b": undefined, "unknown.id": "ctrl+q" },
		{ "tui.select.confirm": ["enter", "ctrl+j"], "tui.select.cancel": ["escape", "ctrl+c"], "app.test.d": ["f6"] },
	];
	const sample = ["", "a", "h", "k", "\x08", "\x02", "\r", "\n", "\x1b", "\x1b[A", "\x1b[B", "\x1b[D", "\x1b[C", "\x14", "\x18", "\x0a", "\x1b[15~", "\x1b[15;2~", "\x1b[17~", "\x03", "\x1b[104;5u", "\x1b[13;1u", "\x1bx", "\t", " ", "\x7f", "\x1b[1;5D", "\x1b[1;3D", "\x1bb", "\x1b[5~", "\x1b[6~", "\x1b[H", "\x1b[F"];
	const cases = [];
	for (const [label, definitions] of [["tui", defs], ["merged", merged]]) {
		for (const user of userSets) {
			const mgr = new keybindings.KeybindingsManager(definitions, user);
			const every = Object.keys(definitions);
			const row = { definitions: label, user: Object.fromEntries(Object.entries(user).filter(([, v]) => v !== undefined)), userWithUndefined: Object.keys(user).filter((k) => user[k] === undefined), keys: {}, resolved: mgr.getResolvedBindings(), conflicts: mgr.getConflicts(), userBindings: mgr.getUserBindings(), matches: {}, definition: {} };
			for (const id of [...every, "no.such.id"]) {
				row.keys[id] = mgr.getKeys(id);
				row.definition[id] = mgr.getDefinition(id) ?? null;
				row.matches[id] = sample.map((d) => mgr.matches(d, id));
			}
			// setUserBindings replaces and rebuilds.
			mgr.setUserBindings({ "tui.editor.cursorLeft": "ctrl+g" });
			row.afterSet = { keys: mgr.getKeys("tui.editor.cursorLeft"), resolved: mgr.getResolvedBindings(), conflicts: mgr.getConflicts(), userBindings: mgr.getUserBindings() };
			cases.push(row);
		}
	}
	const globalDefault = new keybindings.KeybindingsManager(defs);
	const sdkRoot = path.resolve(outDir, "../../../extensions");
	const asList = (k) => (Array.isArray(k) ? k : [k]);
	const lit = (x) => JSON.stringify(x);
	const py = ["# Code generated by test/parity/sdk-bulk/gen.mjs; DO NOT EDIT.", `# pi-tui ${piVersion} TUI_KEYBINDINGS: id -> (default keys, description).`, "", "TUI_KEYBINDING_DEFINITIONS = {"];
	for (const [id, d] of Object.entries(defs)) py.push(`    ${lit(id)}: ([${asList(d.defaultKeys).map(lit).join(", ")}], ${d.description === undefined ? "None" : lit(d.description)}),`);
	py.push("}", "");
	fs.writeFileSync(path.join(sdkRoot, "sdk-py/pig_sdk/tui/_tui_keybindings.py"), py.join("\n"));
	const rs = ["// Code generated by test/parity/sdk-bulk/gen.mjs; DO NOT EDIT.", `// pi-tui ${piVersion} TUI_KEYBINDINGS: (id, default keys, description).`, "", `pub(crate) static TUI_KEYBINDING_DEFINITIONS: [(&str, &[&str], Option<&str>); ${Object.keys(defs).length}] = [`];
	for (const [id, d] of Object.entries(defs)) rs.push(`    (${lit(id)}, &[${asList(d.defaultKeys).map(lit).join(", ")}], ${d.description === undefined ? "None" : `Some(${lit(d.description)})`}),`);
	rs.push("];", "");
	fs.writeFileSync(path.join(sdkRoot, "sdk-rs/src/tui/tui_keybindings.rs"), rs.join("\n"));
	write("tui-keybindings.json", { tuiKeybindings: defs, merged, sample, cases, defaultResolved: keybindings.getKeybindings().getResolvedBindings(), globalDefault: globalDefault.getResolvedBindings() });
}

// ---------------------------------------------------------------- String.prototype.toLowerCase table
// fuzzyMatch and the key-id parser lowercase with JavaScript's toLowerCase, which follows the Unicode version of the runtime's ICU.
// Python and Rust carry older or newer Unicode tables, so the SDKs apply this table (every code point toLowerCase changes) instead of
// their own. U+03A3 is context dependent (Final_Sigma) and is left to the SDKs' own rule.
{
	const pairs = [];
	for (let cp = 0; cp < 0x110000; cp++) {
		if (cp >= 0xd800 && cp <= 0xdfff) continue;
		const s = String.fromCodePoint(cp);
		const lower = s.toLowerCase();
		if (lower !== s) pairs.push([cp, [...lower].map((c) => c.codePointAt(0))]);
	}
	write("tui-lower.json", { pairs });
	const sdks = path.resolve(outDir, "../../../extensions");
	const py = ["# Code generated by test/parity/sdk-bulk/gen.mjs; DO NOT EDIT.", `# String.prototype.toLowerCase of ${meta.node}, Unicode ${meta.unicode}: every code point it changes (except U+03A3, which is contextual).`, "", "JS_LOWER = {"];
	for (const [cp, to] of pairs) if (cp !== 0x3a3) py.push(`    0x${cp.toString(16)}: "${to.map((c) => (c > 0xffff ? "\\U" + c.toString(16).padStart(8, "0") : "\\u" + c.toString(16).padStart(4, "0"))).join("")}",`);
	py.push("}", "");
	fs.writeFileSync(path.join(sdks, "sdk-py/pig_sdk/tui/_lower.py"), py.join("\n"));
	const rs = ["// Code generated by test/parity/sdk-bulk/gen.mjs; DO NOT EDIT.", `// String.prototype.toLowerCase of ${meta.node}, Unicode ${meta.unicode}: every code point it changes (except U+03A3, which is contextual).`, "", "/// `(code point, lowercase)`, sorted by code point.", `pub(crate) static JS_LOWER: [(u32, &str); ${pairs.filter(([cp]) => cp !== 0x3a3).length}] = [`];
	for (const [cp, to] of pairs) if (cp !== 0x3a3) rs.push(`    (0x${cp.toString(16)}, "${to.map((c) => "\\u{" + c.toString(16) + "}").join("")}"),`);
	rs.push("];", "");
	fs.writeFileSync(path.join(sdks, "sdk-rs/src/tui/lower.rs"), rs.join("\n"));
}
