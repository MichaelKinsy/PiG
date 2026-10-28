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

// Each additive record carries one user-facing sentence that the generator copies into the bundle.
func TestAdditiveRecordsHaveOneSentenceSummaries(t *testing.T) {
	ledger, err := os.ReadFile("../../docs/additive-features.md")
	if err != nil {
		t.Fatalf("read additive ledger: %v", err)
	}
	record := regexp.MustCompile(`(?m)^## (D\d+) `)
	summary := regexp.MustCompile(`(?m)^Summary: (.*)$`)
	// A second sentence starts after a stop and a space; inline code and a dotted name such as ui.setLogin do not.
	second := regexp.MustCompile(`[.!?]\s+\S`)
	locs := record.FindAllStringSubmatchIndex(string(ledger), -1)
	if len(locs) == 0 {
		t.Fatal("the additive ledger has no records")
	}
	for i, loc := range locs {
		id := string(ledger[loc[2]:loc[3]])
		end := len(ledger)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		found := summary.FindAllStringSubmatch(string(ledger[loc[1]:end]), -1)
		if len(found) != 1 {
			t.Errorf("%s has %d Summary lines; it needs exactly one", id, len(found))
			continue
		}
		text := strings.TrimSpace(found[0][1])
		switch {
		case !strings.HasSuffix(text, "."):
			t.Errorf("%s Summary must be one sentence that ends with a full stop: %q", id, text)
		case second.MatchString(strings.TrimSuffix(text, ".") + " x"):
			t.Errorf("%s Summary has more than one sentence: %q", id, text)
		case len(text) > 200:
			t.Errorf("%s Summary is %d characters; keep it to 200 or fewer", id, len(text))
		}
	}
}
