package idna

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// testdata/unicode-classes.json holds, for every character class of Pi's idna/pattern/pattern.ts, the code point ranges the Pi regular expression matches under Node 24 (ICU Unicode 17), and the line count and SHA-256 of the NFC checks. It was generated once from Pi commit 6d4b7443 with the script below (`node gen-unicode.mjs > unicode-classes.json`). The test compares each class over every code point from 0 to U+10FFFF, and the NFC lines, with this package's Unicode tables.
//
// import { createHash } from "node:crypto";
// import * as Pattern from "/home/kinsy/nschema-oracle/schema/src/formats/typebox/idna/pattern/pattern.ts";
//
// const predicates = {
// 	permittedCategory: Pattern.RE_PERMITTED_CATEGORY,
// 	combiningMark: Pattern.RE_COMBINING_MARK,
// 	letter: Pattern.RE_LETTER,
// 	markNonspacing: Pattern.RE_MARK_NONSPACING,
// 	markSpacingCombining: Pattern.RE_MARK_SPACING_COMBINING,
// 	markEnclosing: Pattern.RE_MARK_ENCLOSING,
// 	numberDecimal: Pattern.RE_NUMBER_DECIMAL,
// 	scriptGreek: Pattern.RE_SCRIPT_GREEK,
// 	scriptHebrew: Pattern.RE_SCRIPT_HEBREW,
// 	scriptJapanese: Pattern.RE_SCRIPT_JAPANESE,
// 	scriptArabicLetter: Pattern.RE_SCRIPT_ARABIC_LETTER,
// 	europeanNumber: Pattern.RE_EUROPEAN_NUMBER,
// 	arabicIndicDigit: Pattern.RE_ARABIC_INDIC_DIGIT,
// 	virama: Pattern.RE_VIRAMA,
// 	rfc5892Disallowed: Pattern.RE_RFC5892_DISALLOWED,
// 	contextoExceptions: Pattern.RE_CONTEXTO_EXCEPTIONS,
// 	pvalidExceptions: Pattern.RE_PVALID_EXCEPTIONS,
// };
// const ranges = {};
// for (const [name, regexp] of Object.entries(predicates)) {
// 	const list = [];
// 	let start = -1;
// 	for (let cp = 0; cp <= 0x110000; cp++) {
// 		const inSet = cp <= 0x10ffff && regexp.test(String.fromCodePoint(cp));
// 		if (inSet && start < 0) start = cp;
// 		if (!inSet && start >= 0) {
// 			list.push([start, cp - 1]);
// 			start = -1;
// 		}
// 	}
// 	ranges[name] = list;
// }
// const hash = createHash("sha256");
// let lines = 0;
// const hex = (text) => [...text].map((char) => char.codePointAt(0).toString(16)).join(",");
// for (let cp = 0; cp <= 0x10ffff; cp++) {
// 	if (cp >= 0xd800 && cp <= 0xdfff) continue;
// 	const text = String.fromCodePoint(cp);
// 	const nfc = text.normalize("NFC");
// 	const viaNfd = text.normalize("NFD").normalize("NFC");
// 	if (nfc === text && viaNfd === text) continue;
// 	hash.update(`${cp.toString(16)} ${hex(nfc)} ${hex(viaNfd)}\n`);
// 	lines++;
// }
// process.stdout.write(JSON.stringify({ ranges, nfc: { lines, sha256: hash.digest("hex") } }));

type classGolden struct {
	Ranges map[string][][2]rune `json:"ranges"`
	NFC    struct {
		Lines  int    `json:"lines"`
		SHA256 string `json:"sha256"`
	} `json:"nfc"`
}

func readClassGolden(t *testing.T) classGolden {
	t.Helper()
	data, err := os.ReadFile("testdata/unicode-classes.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden classGolden
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func TestPatternClassesMatchPi(t *testing.T) {
	golden := readClassGolden(t)
	predicates := map[string]func(rune) bool{
		"permittedCategory":    RePermittedCategory,
		"combiningMark":        ReCombiningMark,
		"letter":               ReLetter,
		"markNonspacing":       ReMarkNonspacing,
		"markSpacingCombining": ReMarkSpacingCombining,
		"markEnclosing":        ReMarkEnclosing,
		"numberDecimal":        ReNumberDecimal,
		"scriptGreek":          ReScriptGreek,
		"scriptHebrew":         ReScriptHebrew,
		"scriptJapanese":       ReScriptJapanese,
		"scriptArabicLetter":   ReScriptArabicLetter,
		"europeanNumber":       ReEuropeanNumber,
		"arabicIndicDigit":     ReArabicIndicDigit,
		"virama":               ReVirama,
		"rfc5892Disallowed":    ReRfc5892Disallowed,
		"contextoExceptions":   ReContextoExceptions,
		"pvalidExceptions":     RePvalidExceptions,
	}
	if len(golden.Ranges) != len(predicates) {
		t.Fatalf("golden has %d classes, test has %d", len(golden.Ranges), len(predicates))
	}
	for name, predicate := range predicates {
		want := make([]bool, 0x110000)
		for _, r := range golden.Ranges[name] {
			for cp := r[0]; cp <= r[1]; cp++ {
				want[cp] = true
			}
		}
		mismatches := 0
		for cp := rune(0); cp <= 0x10ffff; cp++ {
			if predicate(cp) != want[cp] {
				if mismatches < 5 {
					t.Errorf("%s(U+%04X) = %v, Pi says %v", name, cp, !want[cp], want[cp])
				}
				mismatches++
			}
		}
		if mismatches > 0 {
			t.Errorf("%s: %d mismatching code points", name, mismatches)
		}
	}
}

func TestNFCMatchesPi(t *testing.T) {
	golden := readClassGolden(t)
	hash := sha256.New()
	lines := 0
	hexOf := func(text string) string {
		var parts []string
		for _, r := range text {
			parts = append(parts, fmt.Sprintf("%x", r))
		}
		return strings.Join(parts, ",")
	}
	for cp := rune(0); cp <= 0x10ffff; cp++ {
		if cp >= 0xd800 && cp <= 0xdfff {
			continue
		}
		text := string(cp)
		nfc := norm.NFC.String(text)
		viaNfd := norm.NFC.String(norm.NFD.String(text))
		if nfc == text && viaNfd == text {
			continue
		}
		_, _ = fmt.Fprintf(hash, "%x %s %s\n", cp, hexOf(nfc), hexOf(viaNfd))
		lines++
	}
	if lines != golden.NFC.Lines || hex.EncodeToString(hash.Sum(nil)) != golden.NFC.SHA256 {
		t.Fatalf("NFC lines = %d sha256 = %x, Pi says %d %s", lines, hash.Sum(nil), golden.NFC.Lines, golden.NFC.SHA256)
	}
}
