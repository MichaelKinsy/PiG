package closure

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The number of active divergences is one fact. It was written in five places:
// the ledger heading, the heading-versus-sections gate, and three closure
// denominators. Adding a divergence failed all three denominators at once, in
// tests that name no divergence and give no hint that the ledger is the cause.
//
// The ledger is the source. Read it.

var divergenceSectionRE = regexp.MustCompile(`(?m)^## D\d+ `)

func activeDivergenceCount(t *testing.T) int {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "DIVERGENCES.md"))
	if err != nil {
		t.Fatalf("read DIVERGENCES.md: %v", err)
	}
	n := len(divergenceSectionRE.FindAllIndex(data, -1))
	if n == 0 {
		t.Fatal("DIVERGENCES.md has no `## D<N>` sections; the heading pattern changed")
	}
	return n
}

// Guards the reader itself. A regex that silently stops matching would make
// every denominator agree on a wrong number.
func TestTheDivergenceCounterFindsTheLedgerSections(t *testing.T) {
	if n := activeDivergenceCount(t); n < 20 {
		t.Fatalf("counted %d divergences, which is too few for this ledger; the reader is broken", n)
	}
}
