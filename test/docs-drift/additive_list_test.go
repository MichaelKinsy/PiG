package docsdrift

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The Additive features table in divergences.md is generated from
// docs/additive-features.md by automation/gen/gen-additive-list.py. An agent
// that reads the bundle must see every additive feature, so the table must name
// each ledger record and must equal what the generator renders now.
func TestAdditiveListCoversTheLedger(t *testing.T) {
	ledger, err := os.ReadFile("../../docs/additive-features.md")
	if err != nil {
		t.Fatalf("read additive ledger: %v", err)
	}
	page := docFiles(t)["divergences.md"]
	_, section, ok := strings.Cut(page, "## Additive features")
	if !ok {
		t.Fatal("divergences.md has no Additive features section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	ids := regexp.MustCompile(`(?m)^## (D\d+) `).FindAllStringSubmatch(string(ledger), -1)
	if len(ids) == 0 {
		t.Fatal("the additive ledger has no records")
	}
	for _, m := range ids {
		if !strings.Contains(section, "| "+m[1]+" |") {
			t.Errorf("divergences.md Additive features has no row for %s", m[1])
		}
	}
}

func TestAdditiveListIsFresh(t *testing.T) {
	out, err := exec.Command("python3", "../../automation/gen/gen-additive-list.py", "--check").CombinedOutput()
	if err != nil {
		t.Fatalf("additive list is stale: %v\n%s", err, out)
	}
}
