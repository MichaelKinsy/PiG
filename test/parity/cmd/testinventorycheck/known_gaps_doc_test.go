package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderKnownGapsDerivesTotalsAndRowsFromTheChecker(t *testing.T) {
	inv, m, policy := knownGapReleasePolicy()
	block := renderKnownGaps(inv, m, policy)
	for _, want := range []string{
		knownGapsBegin,
		knownGapsEnd,
		"The checker reports 2 files: 1 ported, 1 partial, 0 scenario-covered, 0 designed-out, 0 divergence and 0 pending.",
		"accepts 1 approved 0.3.x known gap and reports 2 reviewed paths and 1 ported (committed baseline 1). 1 file is open: 1 hot-path and 0 without a hot-path tag.",
		"| providers | `" + inv.Files[0].Path + "` | partial | yes | `FOLLOWUP-providers` |",
		"Approved known gaps by area: tools 0, providers 1, sessions 0, extensions 0, tui 0, cli 0, utilities 0. `make test-porting-release` prints the full missing-case text",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if renderKnownGaps(inv, m, policy) != block {
		t.Fatal("render is not deterministic")
	}
}

func TestRenderKnownGapsOrdersHotPathFirstAndReportsUntaggedRows(t *testing.T) {
	inv, m, policy := knownGapReleasePolicy()
	// The untagged open row has the smaller path, so only the hot-path-first rule puts the hot row before it.
	m.Entries[0].Disposition = "pending"
	m.Entries[1].Disposition = "partial"
	policy.Entries[0].Tags = []string{}
	policy.Entries[1].Tags = []string{"hot-path", "deferred-0.3.x"}
	block := renderKnownGaps(inv, m, policy)
	hot := strings.Index(block, "| utilities | `"+inv.Files[1].Path+"` | partial | yes | `FOLLOWUP-utilities` |")
	untagged := strings.Index(block, "| providers | `"+inv.Files[0].Path+"` | pending | no | none |")
	if hot < 0 || untagged < 0 || hot > untagged {
		t.Fatalf("want the hot-path row before the smaller-path untagged row:\n%s", block)
	}
	if !strings.Contains(block, "2 files are open: 1 hot-path and 1 without a hot-path tag.") {
		t.Fatalf("open-file split missing:\n%s", block)
	}
}

func TestRenderKnownGapsWithNoOpenFiles(t *testing.T) {
	inv, m, policy := closedReleasePolicy()
	block := renderKnownGaps(inv, m, policy)
	if !strings.Contains(block, "No test file is open.") || !strings.Contains(block, "Approved known gaps by area: tools 0, providers 0,") {
		t.Fatalf("closed block wrong:\n%s", block)
	}
}

func TestKnownGapsDriftCheckCatchesStaleBlock(t *testing.T) {
	inv, m, policy := knownGapReleasePolicy()
	block := renderKnownGaps(inv, m, policy)
	path := filepath.Join(t.TempDir(), "gaps.md")
	doc := "# Gaps\n\nhistory before\n\n" + knownGapsBegin + "\nstale\n" + knownGapsEnd + "\n\nhistory after\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncKnownGaps(path, block, true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("check on a stale block = %v, want stale error", err)
	}
	if err := syncKnownGaps(path, block, false); err != nil {
		t.Fatal(err)
	}
	if err := syncKnownGaps(path, block, true); err != nil {
		t.Fatalf("check after write = %v", err)
	}
	written, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(written), "# Gaps\n\nhistory before\n\n") || !strings.HasSuffix(string(written), "\n\nhistory after\n") {
		t.Fatalf("text outside the markers changed:\n%s", written)
	}
	// A hand edit inside the markers, or a changed mapping, is drift.
	edited := strings.Replace(string(written), "partial | yes", "ported | yes", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncKnownGaps(path, block, true); err == nil {
		t.Fatal("hand-edited block passed the drift check")
	}
	if err := os.WriteFile(path, written, 0o600); err != nil {
		t.Fatal(err)
	}
	m.Entries[0].Disposition = "pending"
	if err := syncKnownGaps(path, renderKnownGaps(inv, m, policy), true); err == nil {
		t.Fatal("block for an older mapping passed the drift check")
	}
}

func TestKnownGapsFlagsRequireReleasePolicyBeforeAnyCheck(t *testing.T) {
	err := run([]string{"-check-known-gaps", "x.md"})
	if err == nil || !strings.Contains(err.Error(), "require -release-policy") {
		t.Fatalf("error = %v, want release-policy usage error", err)
	}
}

func TestKnownGapsStaleErrorNamesFirstDifference(t *testing.T) {
	inv, m, policy := knownGapReleasePolicy()
	block := renderKnownGaps(inv, m, policy)
	path := filepath.Join(t.TempDir(), "gaps.md")
	doc := strings.Replace(block, "partial | yes", "ported | yes", 1)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	err := syncKnownGaps(path, block, true)
	if err == nil || !strings.Contains(err.Error(), "first difference: line") || !strings.Contains(err.Error(), "ported | yes") {
		t.Fatalf("error = %v, want first differing line", err)
	}
}

func TestKnownGapsSyncTreatsCRLFAsLF(t *testing.T) {
	inv, m, policy := knownGapReleasePolicy()
	block := renderKnownGaps(inv, m, policy)
	path := filepath.Join(t.TempDir(), "gaps.md")
	lf := "# Gaps\n\n" + block + "\n\nafter\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	if err := os.WriteFile(path, []byte(crlf), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncKnownGaps(path, block, true); err != nil {
		t.Fatalf("current CRLF doc reported stale: %v", err)
	}
	stale := strings.Replace(crlf, "partial | yes", "ported | yes", 1)
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncKnownGaps(path, block, true); err == nil {
		t.Fatal("stale CRLF doc passed the drift check")
	}
	if err := syncKnownGaps(path, block, false); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(path)
	if strings.Contains(string(written), "\r") || string(written) != lf {
		t.Fatalf("write left CRLF or changed text:\n%q", written)
	}
}

func TestSpliceKnownGapsRequiresOneMarkerPair(t *testing.T) {
	for name, doc := range map[string]string{
		"none":      "no markers",
		"reversed":  knownGapsEnd + knownGapsBegin,
		"duplicate": knownGapsBegin + knownGapsEnd + knownGapsBegin + knownGapsEnd,
	} {
		if _, err := spliceKnownGaps(doc, "x"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
