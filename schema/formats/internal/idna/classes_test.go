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

// testdata/unicode-classes.json holds, for every character class of Pi's idna/pattern/pattern.ts, the code point ranges the Pi regular expression matches under Node 24 (ICU Unicode 17), and the line count and SHA-256 of the NFC checks. testdata/gen/gen-classes.mjs generates it from a Pi checkout (`node gen-classes.mjs <pi>/packages/schema unicode-classes.json`). The test compares each class over every code point from 0 to U+10FFFF, and the NFC lines, with this package's Unicode tables.

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
