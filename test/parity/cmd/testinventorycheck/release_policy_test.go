package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func closedReleasePolicy() (inventory, mapping, releasePolicy) {
	inv, m, _, _ := baseline()
	policy := releasePolicy{
		UpstreamVersion: inv.UpstreamVersion,
		BaselineCommit:  strings.Repeat("a", 40),
		BaselinePorted:  []string{inv.Files[0].Path},
		Entries: []policyEntry{
			{Path: inv.Files[0].Path, UpstreamTestHash: hashA, Area: "providers", Tags: []string{"hot-path"}, Rationale: "selected provider request path"},
			{Path: inv.Files[1].Path, UpstreamTestHash: hashB, Area: "utilities", Tags: []string{}, Rationale: "separate server API"},
		},
	}
	return inv, m, policy
}

func TestReleasePolicyAcceptsClosedHotPaths(t *testing.T) {
	inv, m, policy := closedReleasePolicy()
	if err := validateReleasePolicy(inv, m, policy); err != nil {
		t.Fatal(err)
	}
}

func TestReleasePolicyRejectsOpenHotPathsEvenWhenPortedCountIsMaintained(t *testing.T) {
	for _, disposition := range []string{"pending", "partial"} {
		t.Run(disposition, func(t *testing.T) {
			inv, m, policy := closedReleasePolicy()
			m.Entries[0].Disposition = disposition
			m.Entries[0].Rationale = "missing cancellation and ordering cases"
			m.Entries[1].Disposition = "ported"
			err := validateReleasePolicy(inv, m, policy)
			for _, want := range []string{inv.Files[0].Path, "remains " + disposition, "2 upstream case sites", m.Entries[0].Rationale} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error=%v, want %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "count decreased") {
				t.Fatalf("hot-path rejection must not depend on a count decrease: %v", err)
			}
		})
	}
}

func TestReleasePolicyRejectsPortedRegressionOutsideHotPaths(t *testing.T) {
	inv, m, policy := closedReleasePolicy()
	policy.Entries[0].Tags = nil
	m.Entries[0].Disposition = "partial"
	if err := validateReleasePolicy(inv, m, policy); err == nil || !strings.Contains(err.Error(), "count decreased: 0 < committed baseline 1") {
		t.Fatalf("regression error=%v", err)
	}
}

func TestReleasePolicyAllowsUntaggedPendingWithoutCountRegression(t *testing.T) {
	inv, m, policy := closedReleasePolicy()
	m.Entries[1].Disposition = "pending"
	if err := validateReleasePolicy(inv, m, policy); err != nil {
		t.Fatal(err)
	}
}

func TestReleasePolicyRejectsReviewDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*inventory, *mapping, *releasePolicy)
		want   string
	}{
		{"missing path", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries = p.Entries[:1] }, "missing production-path review"},
		{"unknown path", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[1].Path += ".missing" }, "unknown test"},
		{"duplicate path", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[1] = p.Entries[0] }, "sorted and unique"},
		{"unknown tag", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].Tags = []string{"hotpath"} }, "unknown tag"},
		{"duplicate tag", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].Tags = []string{"hot-path", "hot-path"} }, "unique tags"},
		{"unknown area", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].Area = "other" }, "unknown area"},
		{"missing rationale", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].Rationale = "" }, "needs a rationale"},
		{"hash drift", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].UpstreamTestHash = hashB }, "hash changed"},
		{"version drift", func(_ *inventory, _ *mapping, p *releasePolicy) { p.UpstreamVersion = "0.0.0" }, "upstream version"},
		{"missing mapping", func(_ *inventory, m *mapping, _ *releasePolicy) { m.Entries = m.Entries[:1] }, "no test mapping"},
		{"unknown baseline", func(_ *inventory, _ *mapping, p *releasePolicy) { p.BaselinePorted = []string{"unknown"} }, "baseline names unknown"},
		{"duplicate baseline", func(_ *inventory, _ *mapping, p *releasePolicy) {
			p.BaselinePorted = append(p.BaselinePorted, p.BaselinePorted[0])
		}, "baseline paths must be sorted and unique"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv, m, policy := closedReleasePolicy()
			tc.mutate(&inv, &m, &policy)
			if err := validateReleasePolicy(inv, m, policy); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestReleasePolicyVerifiesCommittedBaseline(t *testing.T) {
	inv, m, policy := closedReleasePolicy()
	_, _, evidence, divergences := baseline()
	root, invPath, mapPath := writeRepo(t, inv, m, evidence, divergences)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("add", "mapping.json")
	git("-c", "user.name=Parity Test", "-c", "user.email=parity@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "reviewed baseline")
	policy.BaselineCommit = git("rev-parse", "HEAD")
	policyPath := filepath.Join(root, "policy.json")
	writeJSON(t, policyPath, policy)
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err != nil {
		t.Fatal(err)
	}
	// A release squashed onto an older public commit records ports made since that commit.
	released := m
	released.Entries = slices.Clone(m.Entries)
	released.Entries[1].Disposition = "ported"
	writeJSON(t, mapPath, released)
	extended := policy
	extended.BaselinePorted = []string{inv.Files[0].Path, inv.Files[1].Path}
	writeJSON(t, policyPath, extended)
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err != nil {
		t.Fatalf("baseline above the committed floor: %v", err)
	}
	writeJSON(t, mapPath, m)
	writeJSON(t, policyPath, policy)
	renamed := filepath.Join(root, "moved", filepath.Base(mapPath))
	if err := os.MkdirAll(filepath.Dir(renamed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mapPath, renamed); err != nil {
		t.Fatal(err)
	}
	mapPath = renamed
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err != nil {
		t.Fatalf("renamed mapping must retain the committed baseline: %v", err)
	}
	policy.BaselinePorted = nil
	writeJSON(t, policyPath, policy)
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err == nil || !strings.Contains(err.Error(), "baseline drops 1 ported paths") {
		t.Fatalf("baseline below the committed floor error=%v", err)
	}
	policy.BaselineCommit = strings.Repeat("f", 40)
	writeJSON(t, policyPath, policy)
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err == nil || !strings.Contains(err.Error(), "fetch this commit") {
		t.Fatalf("missing committed baseline error=%v", err)
	}
}

// TestReleasePolicyCarriesTheFloorAcrossAnUpstreamLeap anchors a release that adopts a new upstream version on the last public commit, which maps only the previous version. Every path that commit ported and the new denominator still has must stay ported in the baseline.
func TestReleasePolicyCarriesTheFloorAcrossAnUpstreamLeap(t *testing.T) {
	inv, m, policy := closedReleasePolicy()
	_, _, evidence, divergences := baseline()
	root, invPath, _ := writeRepo(t, inv, m, evidence, divergences)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	// The previous version's mapping is the only one at the anchor; it also ported a path the new denominator dropped.
	previous := mapping{UpstreamVersion: "0.0.1", Entries: []mappingEntry{
		{Path: inv.Files[0].Path, Disposition: "ported"},
		{Path: inv.Files[1].Path, Disposition: "designed-out"},
		{Path: "packages/gone/test/removed.test.ts", Disposition: "ported"},
	}}
	older := mapping{UpstreamVersion: "0.0.0", Entries: []mappingEntry{{Path: inv.Files[1].Path, Disposition: "ported"}}}
	previousPath := filepath.Join(root, "test-mapping-v0.0.1.json")
	writeJSON(t, previousPath, previous)
	writeJSON(t, filepath.Join(root, "test-mapping-v0.0.0.json"), older)
	git("init", "-q")
	git("add", "test-mapping-v0.0.1.json", "test-mapping-v0.0.0.json")
	git("-c", "user.name=Parity Test", "-c", "user.email=parity@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "last public commit")
	policy.BaselineCommit = git("rev-parse", "HEAD")
	policyPath := filepath.Join(root, "policy.json")
	writeJSON(t, policyPath, policy)
	// The current mapping is named for its version, as in the repository.
	currentPath := filepath.Join(root, "test-mapping-v"+inv.UpstreamVersion+".json")
	writeJSON(t, currentPath, m)
	if err := checkReleasePolicy(invPath, currentPath, policyPath, root); err != nil {
		t.Fatalf("anchor that predates the version must carry the previous floor: %v", err)
	}
	// A path the new denominator no longer has is not part of the floor; a dropped port is.
	policy.BaselinePorted = nil
	writeJSON(t, policyPath, policy)
	if err := checkReleasePolicy(invPath, currentPath, policyPath, root); err == nil || !strings.Contains(err.Error(), "baseline drops 1 ported paths") {
		t.Fatalf("baseline below the carried floor error=%v", err)
	}
	// A mapping for a newer version than the policy is not a floor for it, even when it ported more.
	writeJSON(t, filepath.Join(root, "test-mapping-v999.0.0.json"), mapping{UpstreamVersion: "999.0.0", Entries: []mappingEntry{
		{Path: inv.Files[0].Path, Disposition: "ported"},
		{Path: inv.Files[1].Path, Disposition: "ported"},
	}})
	git("add", "test-mapping-v999.0.0.json")
	git("-c", "user.name=Parity Test", "-c", "user.email=parity@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "newer mapping")
	policy.BaselineCommit = git("rev-parse", "HEAD")
	policy.BaselinePorted = []string{inv.Files[0].Path}
	writeJSON(t, policyPath, policy)
	writeJSON(t, currentPath, m)
	if err := checkReleasePolicy(invPath, currentPath, policyPath, root); err != nil {
		t.Fatalf("a newer mapping at the anchor must be ignored in favour of the older one: %v", err)
	}
	// Two mappings of the newest older version leave the carried floor ambiguous, as two exact mappings do.
	if err := os.MkdirAll(filepath.Join(root, "copy"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(root, "copy", "test-mapping-v0.0.1.json"), older)
	git("add", "copy/test-mapping-v0.0.1.json")
	git("-c", "user.name=Parity Test", "-c", "user.email=parity@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "ambiguous older mapping")
	policy.BaselineCommit = git("rev-parse", "HEAD")
	writeJSON(t, policyPath, policy)
	writeJSON(t, currentPath, m)
	if err := checkReleasePolicy(invPath, currentPath, policyPath, root); err == nil || !strings.Contains(err.Error(), "found 2 of test-mapping-v0.0.1.json") {
		t.Fatalf("ambiguous carried floor error=%v", err)
	}
}

// designedOutPolicy returns a closed policy whose first (ported) test designs out one case in both the mapping and the reviewed policy.
func designedOutPolicy() (inventory, mapping, releasePolicy) {
	inv, m, policy := closedReleasePolicy()
	m.Entries[0].DesignedOutCases = []string{"official › runs"}
	m.Entries[0].Rationale = "owner decision: official distribution only"
	policy.Entries[0].DesignedOutCases = []string{"official › runs"}
	policy.Entries[0].Rationale = "selected provider request path; designed-out cases SCRUTINIZED:approved by the owner"
	return inv, m, policy
}

// TestReleasePolicyBindsDesignedOutCasesToReviewedDenominator proves the reviewed
// policy, not the mapping alone, is the denominator for designed-out cases: a
// mapping that drops, adds or changes designedOutCases no longer agrees with the
// approved list, and an approved list needs the owner approval token.
func TestReleasePolicyBindsDesignedOutCasesToReviewedDenominator(t *testing.T) {
	inv, m, policy := designedOutPolicy()
	if err := validateReleasePolicy(inv, m, policy); err != nil {
		t.Fatalf("agreeing designed-out cases rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*mapping, *releasePolicy)
		want   string
	}{
		{"mapping drops the approved list", func(m *mapping, _ *releasePolicy) { m.Entries[0].DesignedOutCases = nil }, "designedOutCases"},
		{"mapping adds an unapproved case", func(m *mapping, p *releasePolicy) {
			p.Entries[0].DesignedOutCases = nil
		}, "designedOutCases"},
		{"mapping names a different case", func(m *mapping, _ *releasePolicy) {
			m.Entries[0].DesignedOutCases = []string{"analytics › ported"}
		}, "designedOutCases"},
		{"approved list lacks the owner approval", func(_ *mapping, p *releasePolicy) {
			p.Entries[0].Rationale = "selected provider request path"
		}, "SCRUTINIZED:approved"},
		// Both lists repeat the id, so the sorted comparison agrees and only the policy uniqueness rule rejects it.
		{"approved list has duplicates", func(m *mapping, p *releasePolicy) {
			m.Entries[0].DesignedOutCases = []string{"official › runs", "official › runs"}
			p.Entries[0].DesignedOutCases = []string{"official › runs", "official › runs"}
		}, "duplicate designedOutCases"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv, m, policy := designedOutPolicy()
			tc.mutate(&m, &policy)
			err := validateReleasePolicy(inv, m, policy)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), inv.Files[0].Path) {
				t.Fatalf("error=%v, want %q for %s", err, tc.want, inv.Files[0].Path)
			}
		})
	}
}
