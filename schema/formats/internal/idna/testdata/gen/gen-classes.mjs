// Generates testdata/unicode-classes.json: for every character class of Pi's idna/pattern/pattern.ts (packages/schema at Pi commit 8ffea8a1, unchanged since 6d4b7443), the code point ranges the Pi regular expression matches, and the line count and SHA-256 of the NFC checks.
// Run through Node 24 against a checkout of Pi: node gen-classes.mjs <pi>/packages/schema <out.json>
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
const root = process.argv[2];
const Pattern = await import(`${root}/src/formats/typebox/idna/pattern/pattern.ts`);

const predicates = {
	permittedCategory: Pattern.RE_PERMITTED_CATEGORY,
	combiningMark: Pattern.RE_COMBINING_MARK,
	letter: Pattern.RE_LETTER,
	markNonspacing: Pattern.RE_MARK_NONSPACING,
	markSpacingCombining: Pattern.RE_MARK_SPACING_COMBINING,
	markEnclosing: Pattern.RE_MARK_ENCLOSING,
	numberDecimal: Pattern.RE_NUMBER_DECIMAL,
	scriptGreek: Pattern.RE_SCRIPT_GREEK,
	scriptHebrew: Pattern.RE_SCRIPT_HEBREW,
	scriptJapanese: Pattern.RE_SCRIPT_JAPANESE,
	scriptArabicLetter: Pattern.RE_SCRIPT_ARABIC_LETTER,
	europeanNumber: Pattern.RE_EUROPEAN_NUMBER,
	arabicIndicDigit: Pattern.RE_ARABIC_INDIC_DIGIT,
	virama: Pattern.RE_VIRAMA,
	rfc5892Disallowed: Pattern.RE_RFC5892_DISALLOWED,
	contextoExceptions: Pattern.RE_CONTEXTO_EXCEPTIONS,
	pvalidExceptions: Pattern.RE_PVALID_EXCEPTIONS,
};
const ranges = {};
for (const [name, regexp] of Object.entries(predicates)) {
	const list = [];
	let start = -1;
	for (let cp = 0; cp <= 0x110000; cp++) {
		const inSet = cp <= 0x10ffff && regexp.test(String.fromCodePoint(cp));
		if (inSet && start < 0) start = cp;
		if (!inSet && start >= 0) {
			list.push([start, cp - 1]);
			start = -1;
		}
	}
	ranges[name] = list;
}
const hash = createHash("sha256");
let lines = 0;
const hex = (text) => [...text].map((char) => char.codePointAt(0).toString(16)).join(",");
for (let cp = 0; cp <= 0x10ffff; cp++) {
	if (cp >= 0xd800 && cp <= 0xdfff) continue;
	const text = String.fromCodePoint(cp);
	const nfc = text.normalize("NFC");
	const viaNfd = text.normalize("NFD").normalize("NFC");
	if (nfc === text && viaNfd === text) continue;
	hash.update(`${cp.toString(16)} ${hex(nfc)} ${hex(viaNfd)}\n`);
	lines++;
}
writeFileSync(process.argv[3], JSON.stringify({ ranges, nfc: { lines, sha256: hash.digest("hex") } }) + "\n");
