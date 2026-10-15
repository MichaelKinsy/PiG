package tools

import (
	"math/rand"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type normalizeForms struct {
	Cp   int    `json:"cp"`
	NFD  string `json:"nfd"`
	NFC  string `json:"nfc"`
	NFKD string `json:"nfkd"`
	NFKC string `json:"nfkc"`
}

// String.prototype.normalize against golang.org/x/text/unicode/norm, which PiG uses where Pi calls normalize: NFKC in the edit tool's fuzzy
// match (edit-diff.ts normalizeForFuzzyMatch) and NFD in the macOS file-name variants (path-utils.ts tryNFDVariant). Every code point, then random
// sequences of the code points that change, which also exercise canonical ordering and composition.
func TestUnicodeNormalizationMatchesNode(t *testing.T) {
	var changed []normalizeForms
	var sequences [][]int
	rng := rand.New(rand.NewSource(7))
	var seeds []int
	// First pass: JS reports every code point whose forms differ from itself.
	pioracle.Run(t, `
const out = [];
for (let cp = 0; cp <= 0x10FFFF; cp++) {
	if (cp >= 0xD800 && cp <= 0xDFFF) continue;
	const s = String.fromCodePoint(cp);
	const nfd = s.normalize("NFD"), nfc = s.normalize("NFC"), nfkd = s.normalize("NFKD"), nfkc = s.normalize("NFKC");
	if (nfd !== s || nfc !== s || nfkd !== s || nfkc !== s) out.push({ cp, nfd, nfc, nfkd, nfkc });
}
emit(out);
`, nil, &changed)
	if len(changed) < 5000 { // Unicode has more than 5000 code points with a decomposition
		t.Fatalf("Node reported only %d code points that normalize", len(changed))
	}
	for _, f := range changed {
		seeds = append(seeds, f.Cp)
	}
	changedByCp := map[int]normalizeForms{}
	for _, f := range changed {
		changedByCp[f.Cp] = f
	}
	bad := 0
	for cp := 0; cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		s := string(rune(cp))
		want, ok := changedByCp[cp]
		if !ok {
			want = normalizeForms{Cp: cp, NFD: s, NFC: s, NFKD: s, NFKC: s}
		}
		got := normalizeForms{Cp: cp, NFD: norm.NFD.String(s), NFC: norm.NFC.String(s), NFKD: norm.NFKD.String(s), NFKC: norm.NFKC.String(s)}
		if got != want {
			if bad++; bad <= 5 {
				t.Errorf("U+%04X: got %+v, want %+v", cp, got, want)
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d code points differ", bad)
	}
	// Second pass: random sequences of changed code points plus ASCII and Hangul jamo.
	extra := []int{'a', 'e', 'o', 0x1100, 0x1161, 0x11A8, 0x0301, 0x0323, 0x0308, 0x0345}
	for range 4000 {
		var seq []int
		for range 1 + rng.Intn(6) {
			if rng.Intn(3) == 0 {
				seq = append(seq, extra[rng.Intn(len(extra))])
			} else {
				seq = append(seq, seeds[rng.Intn(len(seeds))])
			}
		}
		sequences = append(sequences, seq)
	}
	var wants []normalizeForms
	pioracle.Run(t, `
emit(input.map((seq) => {
	const s = String.fromCodePoint(...seq);
	return { cp: 0, nfd: s.normalize("NFD"), nfc: s.normalize("NFC"), nfkd: s.normalize("NFKD"), nfkc: s.normalize("NFKC") };
}));
`, sequences, &wants)
	bad = 0
	for i, seq := range sequences {
		var buf []byte
		for _, cp := range seq {
			buf = utf8.AppendRune(buf, rune(cp))
		}
		s := string(buf)
		got := normalizeForms{NFD: norm.NFD.String(s), NFC: norm.NFC.String(s), NFKD: norm.NFKD.String(s), NFKC: norm.NFKC.String(s)}
		if got != wants[i] {
			if bad++; bad <= 5 {
				t.Errorf("%U: got %+v, want %+v", seq, got, wants[i])
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d sequences differ", bad)
	}
}
