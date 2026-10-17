package util

import (
	"bytes"
	"math/rand"
	"testing"

	upstream "github.com/yuin/goldmark/util"
)

// The fork builds the case-folding map on first use; the folded text must be what upstream goldmark's package-initialized map gives.
func TestFullUnicodeCaseFoldingMatchesUpstream(t *testing.T) {
	pieces := []string{"a", "Z", " ", "\t", "ß", "ẞ", "İ", "ǰ", "ﬃ", "ΐ", "Σ", "ς", "σ", "Å", "Å", "ŉ", "ǅ", "\xff", "😀", "\u0345", "Ⅷ", "ﬆ", "ᾈ"}
	for seed := range int64(3000) {
		random := rand.New(rand.NewSource(seed))
		var text []byte
		for range 1 + random.Intn(10) {
			text = append(text, pieces[random.Intn(len(pieces))]...)
		}
		if got, want := DoFullUnicodeCaseFolding(text), upstream.DoFullUnicodeCaseFolding(text); !bytes.Equal(got, want) {
			t.Fatalf("seed %d %q: got %q want %q", seed, text, got, want)
		}
		if got, want := ToLinkReference(text), upstream.ToLinkReference(text); got != want {
			t.Fatalf("seed %d %q: reference %q want %q", seed, text, got, want)
		}
	}
	if got := string(DoFullUnicodeCaseFolding([]byte("Straße"))); got != "strasse" {
		t.Fatalf("Straße folds to %q", got)
	}
}
