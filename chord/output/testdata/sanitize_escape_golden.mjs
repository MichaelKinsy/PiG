// Generates testdata/sanitize_escape_golden.json: a fixed list of inputs and what Pi's own sanitizeOutput
// (packages/chord/src/output.ts at 6d4b7443, imported as-is with types stripped by Node >= 22.18) returned for each.
// The input list is deterministic (fixed seed, no clock): `--inputs` prints it as JSON without Pi, which
// TestSanitizeOutputGoldenInputsMatchTheGenerator uses to fail when the golden and this list drift apart.
//
//   node sanitize_escape_golden.mjs <pi tree root> > sanitize_escape_golden.json    (Pi tree checked out at 6d4b7443)
//   node sanitize_escape_golden.mjs --inputs                                       (the inputs only)
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const SEED = 0x6d4b7443;
const RANDOM_CASES = 1200;
const MAX_LENGTH = 14;

// mulberry32: a small seeded generator, identical on every platform.
function prng(seed) {
	let state = seed >>> 0;
	return () => {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const ESC = "\x1b";
const fixed = [
	// The three upstream cases (output.test.ts).
	"\x1b[31mERROR\x1b[0m\n",
	"\x1b[1;38;5;208mx\x1b[?25l\x1b[2K\x1b(By\x1b7z",
	"\x1b]8;;https://pi.dev\x07link\x1b]8;;\x1b\\ \x1b]0;title\x07ok",
	// Unterminated and malformed strings.
	"\x1b]unterminated",
	"\x1b]a\x1bb\x07c",
	"\x1bPdcs\x1b\\x",
	"\x1b",
	"\x1b[",
	"plain text, tabs\tand\nnewlines",
	"\\",
];
// Every proper prefix of complete sequences: truncated at each point.
const sequences = [
	"\x1b[1;38;5;208m",
	"\x1b[?25l",
	"\x1b(B",
	"\x1b]8;;https://pi.dev\x07",
	"\x1b]0;title\x1b\\",
	"\x1bPdcs\x1b\\",
	"\x1bXsos\x07",
	"\x1b^pm\x07",
	"\x1b_apc\x1b\\",
];
const truncated = [];
for (const sequence of sequences) for (let length = 1; length < sequence.length; length++) truncated.push(`a${sequence.slice(0, length)}`);

const alphabet = [
	ESC, ESC, ESC, "[", "]", "P", "X", "^", "_", "\x07", "\\", "\\", "text", "m", "~", "@", "\t", "\n", " ", "/", "?", "=", "!", ";", "0", "1", "A", "B", "K",
	"\x00", "\x08", "\x0b", "\x1f", "\ufff9", "\ufffb", "\ufeff", "\u00e9", "\ud83d\ude00", "`",
];
const next = prng(SEED);
const random = [];
for (let i = 0; i < RANDOM_CASES; i++) {
	const length = Math.floor(next() * (MAX_LENGTH + 1));
	let text = "";
	for (let j = 0; j < length; j++) text += alphabet[Math.floor(next() * alphabet.length)];
	random.push(text);
}
const inputs = [...fixed, ...truncated, ...random];

if (process.argv[2] === "--inputs") {
	process.stdout.write(JSON.stringify(inputs));
} else {
	const root = process.argv[2];
	if (root === undefined) throw new Error("usage: sanitize_escape_golden.mjs <pi tree root> | --inputs");
	const { sanitizeOutput } = await import(pathToFileURL(join(root, "packages/chord/src/output.ts")).href);
	const lines = inputs.map((input) => JSON.stringify({ input, output: sanitizeOutput(input) }));
	process.stdout.write(`[\n${lines.join(",\n")}\n]\n`);
}
