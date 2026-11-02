// Oracle for packages/tui/src/index.ts: loads Pi's barrel from the pinned source and prints its runtime export names and the result of calling
// a sample of its pure exports. Usage: node index_exports.mjs <path to packages/tui/src/index.ts>
import { pathToFileURL } from "node:url";

const tui = await import(pathToFileURL(process.argv[2]).href);
const cases = [];
const add = (fn, args, call) => {
	cases.push({ fn, args, out: call() ?? null });
};

tui.setKittyProtocolActive(false);
for (const data of ["a", "\x03", "\x1b[A", "\x1b[1;5C", "\x1b[13;2u", "\x1b[97;5u", "\x1b[27;5;13~", "\x1b[97;5:3u", "\x7f", "\x1b\r", "\x1b[Z"]) {
	add("parseKey", [data], () => tui.parseKey(data));
	add("decodeKittyPrintable", [data], () => tui.decodeKittyPrintable(data));
	add("isKeyRelease", [data], () => tui.isKeyRelease(data));
	add("isKeyRepeat", [data], () => tui.isKeyRepeat(data));
}
for (const data of ["\x1b[97;1:2u", "\x1b[97;1:3u", "\x1b[200~x\x1b[97;1:3u"]) {
	add("isKeyRelease", [data], () => tui.isKeyRelease(data));
	add("isKeyRepeat", [data], () => tui.isKeyRepeat(data));
}
for (const [data, id] of [["\x03", "ctrl+c"], ["\x1b[A", "up"], ["\x1b[1;5C", "ctrl+right"], ["\x1b[13;2u", "shift+enter"], ["a", "ctrl+a"], ["\x1b\r", "alt+enter"], ["\r", "enter"]]) {
	add("matchesKey", [data, id], () => tui.matchesKey(data, id));
}
for (const [query, text] of [["", "anything"], ["abc", "a_b_c"], ["abc", "xxabcxx"], ["abc", "cba"], ["ab", "AB"], ["a1", "1a"], ["tsx", "src/tui.tsx"]]) {
	add("fuzzyMatch", [query, text], () => tui.fuzzyMatch(query, text));
}
for (const query of ["", "ts", "lib", "x"]) {
	const items = ["src/tui.ts", "lib/util.ts", "README.md", "tools.ts"];
	add("fuzzyFilter", [items, query], () => tui.fuzzyFilter(items, query, (item) => item));
}
for (const data of ["\x1b[?997;1n", "\x1b[?997;2n", "\x1b[?997;2n\x1b[?997;1n", "\x1b[?997;3n", "x", ""]) {
	add("parseTerminalColorSchemeReport", [data], () => tui.parseTerminalColorSchemeReport(data));
}
for (const status of [
	{ state: "working" },
	{ state: "idle", app: "pi" },
	{ state: "blocked", app: "pi", kind: "permission", message: "Allow bash?" },
	{ state: "blocked", app: "not valid!", kind: "auth", message: "a\nb\tc" },
]) {
	add("formatProgramStatus", [status], () => tui.formatProgramStatus(status));
}
for (const x of [0, 0.25, 0.5, 0.8, 1]) {
	add("oklabToOkhslLightness", [x], () => tui.oklabToOkhslLightness(x));
}
for (const color of ["#abc", "#A0b1C2", "oklch(0.5 0.1 120)", "okhsl(250 100% 50%)", "#12"]) {
	add("parseColor", [color], () => {
		try {
			const parsed = tui.parseColor(color);
			return { hex: tui.colorToHex(parsed), rgb: tui.colorToRgb(parsed), fg: tui.foregroundAnsi(parsed, "truecolor"), bg: tui.backgroundAnsi(parsed, "256color") };
		} catch (error) {
			return { error: true };
		}
	});
}
add("hyperlink", ["label", "https://example.com/a?b=c"], () => tui.hyperlink("label", "https://example.com/a?b=c"));
for (const [dims, width, cell] of [[{ widthPx: 800, heightPx: 600 }, 40, { widthPx: 9, heightPx: 18 }], [{ widthPx: 10, heightPx: 1000 }, 10, { widthPx: 8, heightPx: 16 }]]) {
	add("calculateImageRows", [dims, width, cell], () => tui.calculateImageRows(dims, width, cell));
}
for (const text of ["hello", "日本語", "\x1b[31mred\x1b[0m", "tab\tx", "e\u0301", "👩‍👩‍👧", ""]) {
	add("visibleWidth", [text], () => tui.visibleWidth(text));
	add("stripTerminalSequences", [text], () => tui.stripTerminalSequences(text));
	add("replaceControlCharacters", [text], () => tui.replaceControlCharacters(text));
}
for (const [text, max, ellipsis, pad] of [["hello world", 5, "...", false], ["hello", 10, "...", true], ["日本語テキスト", 7, "…", false], ["\x1b[31mred text\x1b[0m", 4, "", false]]) {
	add("truncateToWidth", [text, max, ellipsis, pad], () => tui.truncateToWidth(text, max, ellipsis, pad));
}
for (const [text, width] of [["the quick brown fox", 8], ["\x1b[1mbold text that wraps\x1b[0m", 9], ["日本語のテキスト", 6], ["", 5]]) {
	add("wrapTextWithAnsi", [text, width], () => tui.wrapTextWithAnsi(text, width));
}
for (const [line, start, length, strict] of [["hello world", 2, 5, false], ["日本語", 1, 3, true], ["日本語", 1, 3, false], ["\x1b[31mred\x1b[0m text", 1, 4, false]]) {
	add("sliceByColumn", [line, start, length, strict], () => tui.sliceByColumn(line, start, length, strict));
}
{
	const line = "a\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\b";
	for (const column of [0, 1, 4, 5, 9]) {
		add("getOsc8LinkAtColumn", [line, column], () => tui.getOsc8LinkAtColumn(line, column));
	}
}

console.log(JSON.stringify({ names: Object.keys(tui).sort(), cases }));
