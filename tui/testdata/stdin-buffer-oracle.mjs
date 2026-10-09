// Generates stdin-buffer-oracle.json: seeded random chunk sequences run through Pi's StdinBuffer.
// Usage: node stdin-buffer-oracle.mjs <path to @earendil-works/pi-tui/dist/index.js> > stdin-buffer-oracle.json
// An op is {chunk} (process), {flush} (public flush(), returns its sequences), {wait} (let the flush timer fire) or {clear}.
// The log lists, per op, the data/paste events emitted and what flush() returned.
import { pathToFileURL } from "node:url";
const { StdinBuffer } = await import(pathToFileURL(process.argv[2]).href);

let seed = 0x1badc0de;
const random = () => {
	seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
	return seed / 0x100000000;
};
const pick = (list) => list[Math.floor(random() * list.length)];
const atoms = [
	"a", "b", " ", "\r", "\u0003", "\x7f", "é", "日", "😀", "\x1b", "\x1b", "\x1b[", "\x1b[", "[", "A", "1", "9", "7", ";", ":", "m", "u", "~", "<", "M", "O", "P", "\\", "]", "_", "\x07",
	"\x1b[A", "\x1b[1;5C", "\x1b[200~", "\x1b[200~", "\x1b[201~", "\x1b[201~", "\x1b[<0;10;5M", "\x1b[<35;20;5m", "\x1b[<", "\x1b[M", "\x1b[Mabc", "\x1bOP", "\x1bO", "\x1b]0;title\x07", "\x1b]11;rgb:0/0/0\x1b\\",
	"\x1b]", "\x1bP>|x\x1b\\", "\x1bP", "\x1b_Gi=1;OK\x1b\\", "\x1b_", "\x1b\\", "\x1ba", "\x1b\x1b", "\x1b[?62;c", "\x1b[?u", "\x1b[200",
	"\x1b[97u", "\x1b[97;1:1u", "\x1b[97;1:3u", "\x1b[97;1:2u", "\x1b[97;2u", "\x1b[97;1u", "\x1b[98u", "\x1b[31u", "\x1b[32u", "\x1b[9731u", "\x1b[128512u", "\x1b[97:65;1:1u", "\x1b[97:65u", "\x1b[;1u", "\x1b[u", "\x1b[97;;1u", "\x1b[97;1:u", "\x1b[97;1:1:1u", "\x1b[97;1;2u", "\x1b[0097u", "\x1b[1114112u", "\x1b[97:1:1u", "\x1b[97;1:1;1u",
	"paste text", "\n", "\t", "\u{1F600}", "\ud83d", "\ude00",
];
const cases = [];
for (let i = 0; i < 900; i++) {
	const useEsc = random() < 0.5;
	const options = { timeout: 3, escapeTimeout: useEsc ? 1 : 3 };
	const ops = [];
	const count = 1 + Math.floor(random() * 7);
	for (let j = 0; j < count; j++) {
		const r = random();
		if (r < 0.07) ops.push({ flush: true });
		else if (r < 0.2) ops.push({ wait: true });
		else if (r < 0.22) ops.push({ clear: true });
		else {
			let chunk = "";
			const n = 1 + Math.floor(random() * 4);
			for (let k = 0; k < n; k++) chunk += pick(atoms);
			// Lone surrogates cannot cross JSON as WTF-8; keep only whole pairs.
			chunk = chunk.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, "?");
			ops.push({ chunk });
		}
	}
	if (random() < 0.5) ops.push({ wait: true });
	cases.push({ options, ops });
}
// A lone surrogate cannot cross JSON, so output carries it as U+F8FF followed by four hex digits (no input atom contains U+F8FF).
const mark = (s) => s.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, (u) => `\uf8ff${u.charCodeAt(0).toString(16)}`);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
for (const c of cases) {
	const buffer = new StdinBuffer(c.options);
	const events = [];
	buffer.on("data", (s) => events.push(["data", mark(s)]));
	buffer.on("paste", (s) => events.push(["paste", mark(s)]));
	c.log = [];
	for (const op of c.ops) {
		events.length = 0;
		let flushed = null;
		if (op.chunk !== undefined) buffer.process(op.chunk);
		else if (op.flush) flushed = buffer.flush();
		else if (op.wait) await sleep(12);
		else if (op.clear) buffer.clear();
		c.log.push({ events: [...events], flushed: flushed && flushed.map(mark), buffer: mark(buffer.getBuffer()) });
	}
	buffer.destroy();
}
process.stdout.write(JSON.stringify(cases) + "\n");
