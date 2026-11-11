package docsdrift

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The additive-features.md page is generated from docs/additive-features.md by
// automation/gen/gen-additive-list.py. Every ledger record names its audience, and
// the page lists exactly the records written for an author (a person who extends or
// modifies PiG).
func TestAdditivePageListsTheAuthorRecords(t *testing.T) {
	ledger, err := os.ReadFile("../../docs/additive-features.md")
	if err != nil {
		t.Fatalf("read additive ledger: %v", err)
	}
	page, ok := docFiles(t)["additive-features.md"]
	if !ok {
		t.Fatal("the bundle has no additive-features.md page")
	}
	records := ledgerRecords(string(ledger))
	if len(records) == 0 {
		t.Fatal("the additive ledger has no records")
	}
	audience := regexp.MustCompile(`(?m)^Audience: (.*)$`)
	authors := 0
	for _, rec := range records {
		found := audience.FindAllStringSubmatch(rec.body, -1)
		if len(found) != 1 {
			t.Errorf("%s has %d Audience lines; it needs exactly one", rec.id, len(found))
			continue
		}
		row := strings.Contains(page, "| "+rec.id+" |")
		switch found[0][1] {
		case "author":
			authors++
			if !row {
				t.Errorf("additive-features.md has no row for author record %s", rec.id)
			}
		case "runtime":
			if row {
				t.Errorf("additive-features.md lists runtime record %s", rec.id)
			}
		default:
			t.Errorf("%s Audience must be author or runtime, not %q", rec.id, found[0][1])
		}
	}
	if authors == 0 {
		t.Error("no additive record is written for an author; the page would be empty")
	}
}

type ledgerRecord struct{ id, body string }

// ledgerRecords splits the additive ledger at its "## D<N> " headings.
func ledgerRecords(ledger string) []ledgerRecord {
	locs := regexp.MustCompile(`(?m)^## (D\d+) `).FindAllStringSubmatchIndex(ledger, -1)
	var out []ledgerRecord
	for i, loc := range locs {
		end := len(ledger)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, ledgerRecord{id: ledger[loc[2]:loc[3]], body: ledger[loc[1]:end]})
	}
	return out
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
	summary := regexp.MustCompile(`(?m)^Summary: (.*)$`)
	// A second sentence starts after a stop and a space; inline code and a dotted name such as ui.setLogin do not.
	second := regexp.MustCompile(`[.!?]\s+\S`)
	records := ledgerRecords(string(ledger))
	if len(records) == 0 {
		t.Fatal("the additive ledger has no records")
	}
	for _, rec := range records {
		found := summary.FindAllStringSubmatch(rec.body, -1)
		if len(found) != 1 {
			t.Errorf("%s has %d Summary lines; it needs exactly one", rec.id, len(found))
			continue
		}
		text := strings.TrimSpace(found[0][1])
		switch {
		case !strings.HasSuffix(text, "."):
			t.Errorf("%s Summary must be one sentence that ends with a full stop: %q", rec.id, text)
		case second.MatchString(strings.TrimSuffix(text, ".") + " x"):
			t.Errorf("%s Summary has more than one sentence: %q", rec.id, text)
		case len(text) > 200:
			t.Errorf("%s Summary is %d characters; keep it to 200 or fewer", rec.id, len(text))
		}
	}
}
