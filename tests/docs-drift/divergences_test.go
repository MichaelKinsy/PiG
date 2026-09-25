package docsdrift

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The bundle's divergence page is what an agent reads before it assumes Pi
// behavior. Every active divergence in the ledger must appear there.
func TestEveryActiveDivergenceIsInTheBundle(t *testing.T) {
	ledger, err := os.ReadFile("../../DIVERGENCES.md")
	if err != nil {
		t.Fatal(err)
	}
	page := docFiles(t)["divergences.md"]
	active := regexp.MustCompile(`(?m)^## (D\d+) `).FindAllStringSubmatch(string(ledger), -1)
	if len(active) == 0 {
		t.Fatal("no divergence IDs parsed from DIVERGENCES.md; the gate would pass vacuously")
	}
	for _, match := range active {
		if !strings.Contains(page, "| "+match[1]+" |") {
			t.Errorf("divergences.md does not list active divergence %s", match[1])
		}
	}
}
