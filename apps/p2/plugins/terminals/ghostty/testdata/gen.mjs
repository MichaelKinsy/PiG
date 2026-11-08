// Regenerates cases.json (the corpus) and golden.json (what Pi's ghostty.ts answers to it): the Go bindings in the
// parent directory must give the same answers byte for byte.
//
//   PI_TERMINALS=/path/to/pi/apps/p2/plugins/terminals node gen.mjs      (Node >= 23.6: runs the .ts files directly)
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const pi = process.env.PI_TERMINALS;
if (!pi) throw new Error("set PI_TERMINALS to Pi's apps/p2/plugins/terminals");
const { Ghostty } = await import(join(pi, "ghostty.ts"));
const wasm = readFileSync(join(pi, "vendor/ghostty/ghostty-vt.wasm"));

const ESC = "\x1b";
const text = (s, extra = {}) => ({ op: "write", s, ...extra });
const frame = (all = false) => ({ op: "frame", all });
const key = (code, k, mods = {}) => ({ op: "key", code, key: k, ...mods });
const mouse = (action, button, x, y, mods = {}) => ({
	op: "mouse",
	action,
	...(button ? { button } : {}),
	x,
	y,
	width: 640,
	height: 384,
	cellWidth: 8,
	cellHeight: 16,
	...mods,
});
const probes = [{ op: "alt" }, { op: "mouseTracking" }, { op: "title" }, { op: "scrollbar" }, { op: "text" }, { op: "snapshot" }];

/** The inputs the Pi terminal tests give the emulator, then probes of every path through ghostty.ts. */
const cases = [
	{
		name: "terminals-host: the shell's output replays from a snapshot (test/p2/terminals-host.test.ts)",
		cols: 40,
		rows: 10,
		scrollback: 100,
		ops: [
			text("$ printf 'in %s\\n' \"$PWD\"; echo $TERM\r\nin /tmp/p2-terminal-cwd-abcdef\r\nxterm-256color\r\n$ "),
			frame(true),
			frame(),
			...probes,
			{ op: "replay", cols: 40, rows: 10, scrollback: 100 },
		],
	},
	{
		name: "terminals-host: 400 lines with a truecolor foreground per cell (test/p2/terminals-host.test.ts)",
		cols: 80,
		rows: 24,
		scrollback: 1000,
		ops: [
			...Array.from({ length: 400 }, (_, i) =>
				text(
					`${Array.from({ length: 79 }, (_, j) => `${ESC}[38;2;${i % 256};${j * 3};${(i * j) % 256}mx`).join("")}${ESC}[0m\r\n`,
				),
			),
			frame(),
			...probes,
			{ op: "scroll", delta: -10 },
			{ op: "scrollbar" },
			frame(),
			{ op: "scroll", delta: "bottom" },
			{ op: "scrollbar" },
			frame(),
			{ op: "replay", cols: 80, rows: 24, scrollback: 1000 },
		],
	},
	{
		name: "terminal-clipboard: 80 lines of scrollback, mouse and paste modes (test/p2/terminal-clipboard.test.ts)",
		cols: 80,
		rows: 24,
		scrollback: 1000,
		ops: [
			{ op: "paste", s: "plain\nlines" },
			text(`${ESC}[?2004h`),
			{ op: "mode", value: 2004 },
			{ op: "paste", s: "first\nsecond\u001bthird" },
			{ op: "paste", s: "toolbar\npaste" },
			{ op: "paste", s: "" },
			{ op: "paste", s: "x".repeat(5000) },
			{ op: "paste", s: "a\u0000b\u0003c\u007fd\r\ne" },
			text(Array.from({ length: 80 }, (_, i) => `copy-line-${i}\r\n`).join("")),
			frame(),
			{ op: "scroll", delta: -20 },
			frame(),
			{ op: "scroll", delta: 5 },
			frame(),
			{ op: "mouseTracking" },
			mouse("press", "left", 100, 100),
			text(`${ESC}[?1000h`),
			{ op: "mouseTracking" },
			{ op: "mode", value: 1000 },
			mouse("press", "left", 100, 100),
			mouse("release", "left", 100, 100),
			mouse("press", "wheelUp", 100, 100),
			mouse("press", "wheelDown", 100, 100),
			mouse("motion", undefined, 120, 100),
			mouse("motion", "left", 120, 140),
			text(`${ESC}[?1002h`),
			mouse("motion", "left", 140, 150),
			mouse("motion", "left", 140, 150),
			mouse("motion", undefined, 200, 150),
			text(`${ESC}[?1003h`),
			mouse("motion", undefined, 220, 170),
			text(`${ESC}[?1006h`),
			mouse("press", "right", 330, 200, { shift: true }),
			mouse("press", "middle", 330, 200, { ctrl: true, alt: true }),
			mouse("release", "middle", 331.7, 200.4),
			mouse("press", "left", -5, -5),
			mouse("press", "left", 5000, 5000),
			text(`${ESC}[?1015h`),
			mouse("press", "left", 100, 100),
			text(`${ESC}[?1005h`),
			mouse("press", "left", 100, 100),
			text(`${ESC}[?1000l${ESC}[?1002l${ESC}[?1003l`),
			{ op: "mouseTracking" },
		],
	},
	{
		name: "terminal-keys: tmux key names encode as a terminal sends them (test/p2/terminal-keys.test.ts)",
		cols: 20,
		rows: 4,
		scrollback: 10,
		ops: [
			key("Enter", "Enter"),
			key("Escape", "Escape"),
			key("Tab", "Tab"),
			key("Tab", "Tab", { shift: true }),
			key("Backspace", "Backspace"),
			key("Space", " "),
			key("KeyC", "c", { ctrl: true }),
			key("KeyC", "c", { ctrl: true, shift: true }),
			key("KeyD", "d", { ctrl: true }),
			key("Space", " ", { ctrl: true }),
			key("Backslash", "\\", { ctrl: true }),
			key("Minus", "_", { ctrl: true }),
			key("KeyX", "x", { alt: true }),
			key("KeyX", "X", { alt: true, shift: true }),
			key("ArrowUp", "ArrowUp"),
			key("ArrowUp", "ArrowUp", { shift: true }),
			key("Home", "Home"),
			key("PageUp", "PageUp"),
			key("Delete", "Delete"),
			key("F1", "F1"),
			key("F5", "F5"),
			text(`${ESC}[?1h`),
			key("ArrowUp", "ArrowUp"),
			key("ArrowDown", "ArrowDown", { ctrl: true }),
			key("Home", "Home"),
		],
	},
	{
		name: "keys: every named key, with modifiers, characters and repeats",
		cols: 20,
		rows: 4,
		scrollback: 10,
		ops: [
			...[
				"Backquote Digit0 Digit1 Digit9 KeyA KeyZ Minus Equal Quote Semicolon Slash Comma Period BracketLeft BracketRight IntlBackslash IntlRo IntlYen AltLeft ControlLeft ShiftLeft MetaLeft CapsLock ContextMenu Convert KanaMode NonConvert End Help Insert PageDown ArrowLeft ArrowRight NumLock Numpad0 Numpad9 NumpadAdd NumpadEnter NumpadDecimal NumpadDivide NumpadMultiply NumpadSubtract NumpadUp NumpadHome F2 F3 F4 F6 F7 F8 F9 F10 F11 F12 F13 F24 F25 Fn FnLock PrintScreen ScrollLock Pause NoSuchKey".split(" "),
			].flatMap(([...names]) =>
				names.flatMap((code) => [
					key(code, ""),
					key(code, code),
					key(code, "a"),
					key(code, "A", { shift: true }),
					key(code, "1", { ctrl: true }),
					key(code, "é", { alt: true }),
					key(code, "x", { meta: true }),
					key(code, "x", { rep: true }),
				]),
			),
			key("", "é"),
			key("", "ß"),
			key("", "İ"),
			key("", "😀"),
			key("", "ab"),
			key("", "\u0080"),
			key("", "\u009f"),
			key("", "\u007f"),
			key("", "\u001f"),
			key("", " "),
			key("", "~"),
			key("", "Dead"),
			key("KeyE", "é", { alt: true }),
			key("Digit5", "%", { shift: true }),
			key("Enter", "\r"),
			key("Backspace", "\u007f", { ctrl: true }),
			text(`${ESC}[>1u`),
			key("KeyA", "a", { ctrl: true }),
			key("KeyA", "A", { shift: true }),
			key("Escape", "Escape"),
			key("Enter", "Enter", { shift: true }),
			key("ShiftLeft", "Shift", { shift: true }),
			key("KeyA", "a", { rep: true }),
			text(`${ESC}[<u${ESC}[?1h${ESC}=`),
			key("ArrowLeft", "ArrowLeft", { alt: true }),
			key("Numpad5", "5"),
			key("NumpadEnter", "Enter"),
		],
	},
	{
		name: "styles, colors, wide characters and graphemes in frames",
		cols: 30,
		rows: 8,
		scrollback: 50,
		colors: { foreground: 0xe0e0e0, background: 0x101010, cursor: 0xff8800 },
		ops: [
			frame(true),
			text(`${ESC}[1mbold${ESC}[0m ${ESC}[2mfaint${ESC}[0m ${ESC}[3mitalic${ESC}[0m ${ESC}[4munder${ESC}[0m ${ESC}[4:3mcurly${ESC}[0m\r\n`),
			text(`${ESC}[5mblink${ESC}[25m ${ESC}[7minv${ESC}[27m ${ESC}[8minvis${ESC}[28m ${ESC}[9mstrike${ESC}[29m ${ESC}[53mover${ESC}[55m\r\n`),
			text(`${ESC}[31mred${ESC}[42m on green${ESC}[0m ${ESC}[38;5;208mc208${ESC}[48;5;21m bg21${ESC}[0m ${ESC}[38;2;1;2;3mtc${ESC}[48;2;250;251;252m bg${ESC}[0m\r\n`),
			text("wide: 日本語 ＡＢ end\r\n"),
			text("emoji: 😀 👨‍👩‍👧 🇯🇵 é e\u0301 \u0915\u094d\u0937\r\n"),
			text("tab\there\r\n"),
			frame(),
			frame(),
			frame(true),
			text(`${ESC}[2;3H${ESC}[5 q`),
			frame(),
			text(`${ESC}[3 q`),
			frame(),
			text(`${ESC}[1 q`),
			frame(),
			text(`${ESC}[2 q`),
			frame(),
			text(`${ESC}[?12h`),
			frame(),
			text(`${ESC}[?25l`),
			frame(),
			text(`${ESC}[?25h${ESC}]12;#123456\x07`),
			frame(),
			text(`${ESC}]112\x07`),
			frame(),
			text(`${ESC}[1;29H日`),
			frame(),
			text(`${ESC}[1;30H日`),
			frame(),
			{ op: "setColors", foreground: 0x112233, background: 0xeeeeee },
			frame(true),
			{ op: "setColors", foreground: 0x000000, background: 0xffffff, cursor: 0x00ff00 },
			frame(true),
			text(`${ESC}]10;#445566\x07${ESC}]11;#aabbcc\x07`),
			frame(true),
			{ op: "resize", cols: 12, rows: 5 },
			frame(),
			frame(true),
			...probes,
			{ op: "resize", cols: 50, rows: 12 },
			frame(),
			...probes,
			{ op: "replay", cols: 50, rows: 12, scrollback: 50 },
		],
	},
	{
		name: "wrapping, scroll regions, alternate screen, titles, charsets",
		cols: 20,
		rows: 6,
		scrollback: 30,
		ops: [
			text("a very long line that wraps around the twenty columns of this terminal\r\nnext\r\n"),
			frame(),
			text(`${ESC}[2;5r${ESC}[5;1Hscroll\r\nregion\r\n`),
			frame(),
			text(`${ESC}[r${ESC}]0;title one\x07`),
			{ op: "title" },
			text(`${ESC}]2;日本語 title\x07`),
			{ op: "title" },
			text(`${ESC}]2;\x07`),
			{ op: "title" },
			text(`${ESC}(0lqqk${ESC}(B\r\n`),
			frame(),
			text(`${ESC}[?1049h${ESC}[2J${ESC}[Halt screen\r\n`),
			{ op: "alt" },
			frame(),
			...probes,
			text(`${ESC}[?1049l`),
			{ op: "alt" },
			frame(),
			text(`${ESC}[?7l${"z".repeat(30)}${ESC}[?7h`),
			{ op: "mode", value: 7 },
			{ op: "mode", value: 4, ansi: true },
			{ op: "mode", value: 20, ansi: true },
			{ op: "mode", value: 25 },
			{ op: "mode", value: 1 },
			{ op: "mode", value: 2004 },
			{ op: "mode", value: 0x7fff },
			{ op: "mode", value: 0x1f700 },
			text(`${ESC}[4h${ESC}[?1h${ESC}[?2004h${ESC}[?1004h${ESC}[?2026h`),
			{ op: "mode", value: 4, ansi: true },
			{ op: "mode", value: 1 },
			{ op: "mode", value: 1004 },
			{ op: "mode", value: 2026 },
			...probes,
			text(`${ESC}c`),
			...probes,
		],
	},
	{
		name: "replies: device attributes, cursor position, status and color scheme queries",
		cols: 20,
		rows: 6,
		scrollback: 10,
		replies: true,
		scheme: "dark",
		ops: [
			text(`${ESC}[c`),
			text(`${ESC}[>c`),
			text(`${ESC}[6n`),
			text(`${ESC}[3;7H${ESC}[6n`),
			text(`${ESC}[5n`),
			text(`${ESC}[?996n`),
			text(`${ESC}[?6n`),
			text(`${ESC}[18t${ESC}[14t`),
			text(`${ESC}[?u`),
			text(`${ESC}]10;?\x07${ESC}]11;?\x07`),
			text(`${ESC}P+q544e\x1b\\`),
			text(`${ESC}[?2004$p${ESC}[4$p`),
			text("a".repeat(30) + `${ESC}[6n`),
			// Split across writes: a sequence and a multi-byte character continue in the next write.
			text(`${ESC}[`),
			text("6"),
			text("n"),
			text("日".slice(0, 1)),
		],
	},
	{
		name: "replies: the color scheme query without a scheme callback answer",
		cols: 20,
		rows: 6,
		scrollback: 10,
		replies: true,
		scheme: null,
		ops: [text(`${ESC}[?996n`), text(`${ESC}[6n`)],
	},
	{
		name: "replies: light color scheme",
		cols: 20,
		rows: 6,
		scrollback: 10,
		replies: true,
		scheme: "light",
		ops: [text(`${ESC}[?996n`)],
	},
	{
		name: "a 1 MB stream: wrapped text, colors and scrollback",
		cols: 100,
		rows: 30,
		scrollback: 5000,
		ops: [
			text(
				`${Array.from({ length: 40 }, (_, j) => `${ESC}[${31 + (j % 7)}mline ${j} of the stream, with some text ${"x".repeat(j % 13)}${ESC}[0m`).join(" ")}\r\n`,
				{ repeat: 450 },
			),
			frame(),
			{ op: "scrollbar" },
			...probes,
			{ op: "replay", cols: 100, rows: 30, scrollback: 5000 },
		],
	},
	{
		name: "utf-8 split at every byte of a write",
		cols: 20,
		rows: 4,
		scrollback: 10,
		ops: [
			...[...Buffer.from("é日😀👨‍👩‍👧x", "utf8")].map((byte) => ({ op: "write", b: [byte] })),
			{ op: "write", b: [0xff, 0xfe, 0x41, 0xc3] },
			{ op: "write", b: [0x28] },
			frame(),
			...probes,
		],
	},
	{
		name: "empty writes, resize to the same size, zero-width and out-of-range input",
		cols: 10,
		rows: 3,
		scrollback: 0,
		ops: [
			{ op: "write", b: [] },
			frame(),
			{ op: "resize", cols: 10, rows: 3 },
			text("one\r\ntwo\r\nthree\r\nfour\r\nfive\r\n"),
			frame(true),
			{ op: "scrollbar" },
			{ op: "scroll", delta: -100 },
			{ op: "scrollbar" },
			{ op: "scroll", delta: 100 },
			{ op: "scrollbar" },
			...probes,
		],
	},
];

const b64 = (bytes) => (bytes === undefined ? null : Buffer.from(bytes).toString("base64"));
const digest = (bytes) => ({
	sha256: createHash("sha256").update(bytes).digest("hex"),
	length: bytes.length,
	...(bytes.length <= 4096 ? { base64: b64(bytes) } : {}),
});
const input = (op) => (op.b ? Uint8Array.from(op.b) : new TextEncoder().encode(op.s));

function run(vt, replies, op, replayer) {
	switch (op.op) {
		case "write": {
			const bytes = input(op);
			for (let i = 0; i < (op.repeat ?? 1); i++) vt.write(bytes);
			return { replies: replies.splice(0).map(b64) };
		}
		case "resize":
			vt.resize(op.cols, op.rows);
			return null;
		case "setColors":
			vt.setColors({ foreground: op.foreground, background: op.background, ...(op.cursor === undefined ? {} : { cursor: op.cursor }) });
			return null;
		case "frame":
			return vt.frame(op.all);
		case "snapshot":
			return digest(vt.snapshot());
		case "text":
			return vt.text();
		case "title":
			return vt.title();
		case "mode":
			return vt.mode(op.value, op.ansi ?? false);
		case "alt":
			return vt.alternateScreen();
		case "mouseTracking":
			return vt.mouseTracking();
		case "scrollbar":
			return vt.scrollbar();
		case "scroll":
			vt.scroll(op.delta);
			return null;
		case "key":
			return b64(vt.encodeKey({ code: op.code, key: op.key, shift: op.shift, ctrl: op.ctrl, alt: op.alt, meta: op.meta, repeat: op.rep }));
		case "mouse":
			return b64(vt.encodeMouse(op));
		case "paste":
			return b64(vt.encodePaste(op.s));
		case "replay":
			return replayer(vt, op);
	}
	throw new Error(`unknown op ${op.op}`);
}

const ghostty = await Ghostty.load(wasm);
const golden = [];
for (const c of cases) {
	const replies = [];
	const options = {
		cols: c.cols,
		rows: c.rows,
		scrollback: c.scrollback,
		...(c.colors ? { colors: c.colors } : {}),
		...(c.replies ? { onReply: (bytes) => replies.push(bytes.slice()) } : {}),
		...(c.scheme !== undefined ? { colorScheme: () => c.scheme ?? undefined } : {}),
	};
	const vt = ghostty.terminal(options);
	const results = c.ops.map((op) =>
		run(vt, replies, op, (source, replay) => {
			// A fresh terminal fed the snapshot shows the same screen, as the host's reconnecting viewers see it.
			const fresh = ghostty.terminal({ cols: replay.cols, rows: replay.rows, scrollback: replay.scrollback });
			fresh.write(source.snapshot());
			const all = fresh.frame(true);
			const result = { frame: all, snapshot: digest(fresh.snapshot()) };
			fresh.free();
			return result;
		}),
	);
	vt.free();
	golden.push({ name: c.name, results });
}
writeFileSync(join(here, "cases.json"), `${JSON.stringify(cases)}\n`);
writeFileSync(join(here, "golden.json"), `${JSON.stringify(golden)}\n`);
console.log(`${cases.length} cases, ${cases.reduce((n, c) => n + c.ops.length, 0)} ops`);
