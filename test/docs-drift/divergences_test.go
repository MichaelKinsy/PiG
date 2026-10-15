package docsdrift

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The bundle's divergence page is what an agent reads before it assumes Pi
// behavior. Every active divergence in the ledger must appear there.
func TestEveryActiveDivergenceIsInTheBundle(t *testing.T) {
	ledger, err := os.ReadFile("../../docs/parity/DIVERGENCES.md")
	if err != nil {
		t.Fatal(err)
	}
	page := docFiles(t)["divergences.md"]
	active := regexp.MustCompile(`(?m)^## (D\d+) `).FindAllStringSubmatch(string(ledger), -1)
	if len(active) == 0 {
		t.Fatal("no divergence IDs parsed from docs/parity/DIVERGENCES.md; the gate would pass vacuously")
	}
	for _, match := range active {
		if !strings.Contains(page, "| "+match[1]+" |") {
			t.Errorf("divergences.md does not list active divergence %s", match[1])
		}
	}
}

// docs/parity/DIVERGENCES.md is generated from the one-file-per-record sources in docs/parity/divergences. A record
// edited without regenerating, or a ledger edited by hand, leaves the two different.
func TestDivergenceLedgerEqualsItsRecordFiles(t *testing.T) {
	cmd := exec.Command("go", "run", "./automation/ci/divsplit", "-check")
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
