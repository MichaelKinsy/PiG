package widthx

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// referenceNeedsGraphemeWidth is the previous strings.ContainsAny form of
// needsGraphemeWidth, kept as the oracle for the ASCII-skipping scan.
func referenceNeedsGraphemeWidth(s string) bool {
	if strings.ContainsAny(s, "ำຳ️") {
		return true
	}
	for _, r := range s {
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			return true
		}
	}
	return false
}

func assertNonASCIIScansMatchReference(t *testing.T, s string) {
	t.Helper()
	if got, want := needsGraphemeWidth(s), referenceNeedsGraphemeWidth(s); got != want {
		t.Fatalf("needsGraphemeWidth(%q) = %v, reference %v", s, got, want)
	}
	if got, want := containsThaiLaoAM(s), strings.ContainsAny(s, "ำຳ"); got != want {
		t.Fatalf("containsThaiLaoAM(%q) = %v, reference %v", s, got, want)
	}
}

func TestNonASCIIScansMatchContainsAny(t *testing.T) {
	styled := "\x1b[1m\x1b]8;;https://x\x07link\x1b]8;;\x07\x1b[0m plain ascii"
	assertNonASCIIScansMatchReference(t, "")
	assertNonASCIIScansMatchReference(t, styled)
	for _, piece := range graphemeIterPieces {
		assertNonASCIIScansMatchReference(t, piece)
		assertNonASCIIScansMatchReference(t, styled+piece)
		assertNonASCIIScansMatchReference(t, piece+styled)
		assertNonASCIIScansMatchReference(t, "é"+styled+piece)
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for range 20000 {
		var b strings.Builder
		for range 1 + rng.IntN(10) {
			b.WriteString(graphemeIterPieces[rng.IntN(len(graphemeIterPieces))])
		}
		assertNonASCIIScansMatchReference(t, b.String())
	}
}

func FuzzNonASCIIScansMatchContainsAny(f *testing.F) {
	for _, piece := range graphemeIterPieces {
		f.Add("ab" + piece + "c")
	}
	f.Fuzz(assertNonASCIIScansMatchReference)
}
