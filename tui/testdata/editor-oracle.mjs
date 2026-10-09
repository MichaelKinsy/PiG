// Generates editor-oracle.json.gz: seeded random key sequences run through Pi's Editor (no autocomplete provider).
// Usage: node editor-oracle.mjs <path to @earendil-works/pi-tui/dist/index.js> | gzip -9n > editor-oracle.json.gz
// Each op is {keys}, {setText}, {history} or {render: width}. The log records text, expanded text, cursor, lines,
// submit/change callbacks and, for render ops, the rendered lines (identity border color, focused editor).
import { pathToFileURL } from "node:url";
const { Editor } = await import(pathToFileURL(process.argv[2]).href);

let seed = 0xed170001;
const random = () => {
	seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
	return seed / 0x100000000;
};
const pick = (list) => list[Math.floor(random() * list.length)];
const text = ["a", "b", "c", "d", " ", " ", "  ", "hello", "wörld", "日本語", "日本", "。", "，", "😀", "👍🏽", "e\u0301", "foo.bar", "a-b_c", "/x", "@y", "x\\", "\\", "한국어", "ก็", "ab cd ef gh ij kl mn op qr", "https://example.com/a/very/long/path?x=1", "😀😀😀😀😀😀😀😀", "\u{1F1FA}\u{1F1F8}"];
const keys = [
	"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x1b[H", "\x1b[F", "\x01", "\x05", "\x1b[1;5D", "\x1b[1;5C", "\x1bb", "\x1bf", "\x1b[1;3D", "\x1b[1;3C",
	"\x17", "\x1b\x7f", "\x1bd", "\x0b", "\x15", "\x19", "\x1by", "\x7f", "\x7f", "\x1b[3~", "\x1b[3;5~", "\x1b[3;3~", "\x1f", "\x1b[45;5u", "\x1a",
	"\r", "\r", "\x1b[13;2u", "\x1b\r", "\n", "\x1b[27;2;13~", "\x1b[5~", "\x1b[6~", "\x1d", "\x1b\x1d", "\x1b[1;2D", "\x1b[1;2C", "\x02", "\x06", "\x0e", "\x10", "\x04", "\t",
	"\x1b[99;5u", "\x1b[97;3u", "\x1b[65;2u", "\x1b[27;5;98~", "\x1b[200~short paste\x1b[201~", "\x1b[200~line1\nline2\nline3\x1b[201~", "\x1b",
	"\x1b[200~" + Array.from({ length: 14 }, (_, i) => `row ${i}`).join("\n") + "\x1b[201~",
	"\x1b[200~" + "x".repeat(1100) + "\x1b[201~", "\x1b[200~tab\there\x1b[201~", "\x1b[200~crlf\r\nline\r\n\x1b[201~", "\x1b[200~", "\x1b[201~", "partial", "\x1b[Z",
];
const cases = [];
for (let i = 0; i < 700; i++) {
	const options = { paddingX: pick([0, 0, 0, 1, 2]) };
	const ops = [];
	const count = 6 + Math.floor(random() * 26);
	for (let j = 0; j < count; j++) {
		const r = random();
		if (r < 0.03) ops.push({ setText: pick(["", "one\ntwo", "日本語 text\nsecond line is longer than the first one by quite a lot", "\n\n", "x".repeat(60) + " tail", "a\r\nb", "[paste #1 +20 lines]", "tab\tin"]) });
		else if (r < 0.06) ops.push({ history: pick(["", "first", "multi\nline", "日本語", "first"]) });
		else if (r < 0.2) ops.push({ render: pick([8, 12, 20, 33, 40, 80]) });
		else if (r < 0.55) ops.push({ keys: pick(text) });
		else ops.push({ keys: pick(keys) });
	}
	ops.push({ render: pick([10, 24, 40]) });
	cases.push({ options, ops });
}
const mark = (s) => s.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, (u) => `\uf8ff${u.charCodeAt(0).toString(16)}`);
for (const c of cases) {
	const tui = { requestRender() {}, terminal: { rows: 16, columns: 80 } };
	const editor = new Editor(tui, { borderColor: (s) => s, selectList: {} }, c.options);
	editor.focused = true;
	let callbacks = [];
	editor.onSubmit = (t) => callbacks.push(["submit", mark(t)]);
	editor.onChange = (t) => callbacks.push(["change", mark(t)]);
	c.log = [];
	for (const op of c.ops) {
		callbacks = [];
		let rendered = null;
		let error = null;
		try {
			if (op.keys !== undefined) editor.handleInput(op.keys);
			else if (op.setText !== undefined) editor.setText(op.setText);
			else if (op.history !== undefined) editor.addToHistory(op.history);
			else if (op.render !== undefined) rendered = editor.render(op.render).map(mark);
		} catch (e) {
			error = String(e && e.message);
		}
		c.log.push({ text: mark(editor.getText()), expanded: mark(editor.getExpandedText()), cursor: editor.getCursor(), lines: editor.getLines().map(mark), callbacks, rendered, error });
	}
}
process.stdout.write(JSON.stringify(cases) + "\n");
