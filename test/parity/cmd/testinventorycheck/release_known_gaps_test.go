package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func knownGapReleasePolicy() (inventory, mapping, releasePolicy) {
	inv, m, policy := closedReleasePolicy()
	m.Entries[0].Disposition = "partial"
	m.Entries[0].Rationale = "missing cancellation and ordering cases"
	m.Entries[1].Disposition = "ported" // Preserve the independent committed count.
	policy.Entries[0].Tags = append(policy.Entries[0].Tags, "deferred-0.3.x")
	policy.Entries[0].Rationale = "0.3.x known gap: acceptance incomplete; SCRUTINIZED:approved by Michael Kinsy 2026-09-28; FOLLOWUP-providers; Missing cases: test/parity/interfaces/test-mapping-v" + inv.UpstreamVersion + ".json#" + inv.Files[0].Path
	return inv, m, policy
}

func TestReleaseKnownGapPreservesOpenDisposition(t *testing.T) {
	for _, disposition := range []string{"partial", "pending"} {
		t.Run(disposition, func(t *testing.T) {
			inv, m, policy := knownGapReleasePolicy()
			m.Entries[0].Disposition = disposition
			before := fmt.Sprintf("%#v", m)
			if err := validateReleasePolicy(inv, m, policy); err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%#v", m); got != before {
				t.Fatal("known-gap approval must not mutate mapping dispositions or evidence")
			}
		})
	}
}

func TestReleaseKnownGapRejectsInvalidOrStaleApproval(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*inventory, *mapping, *releasePolicy)
		want   string
	}{
		{"no hot-path tag", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].Tags = []string{"deferred-0.3.x"} }, "stale 0.3.x known gap"},
		{"closed mapping", func(_ *inventory, m *mapping, _ *releasePolicy) { m.Entries[0].Disposition = "ported" }, "stale 0.3.x known gap"},
		{"no reason", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].Rationale = "blanket waiver" }, "needs an explicit reason"},
		{"no approval", func(_ *inventory, _ *mapping, p *releasePolicy) {
			p.Entries[0].Rationale = strings.ReplaceAll(p.Entries[0].Rationale, "SCRUTINIZED:approved", "pending")
		}, "needs an explicit reason"},
		{"wrong follow-up", func(_ *inventory, _ *mapping, p *releasePolicy) {
			p.Entries[0].Rationale = strings.ReplaceAll(p.Entries[0].Rationale, "FOLLOWUP-providers", "FOLLOWUP-tui")
		}, "needs an explicit reason"},
		{"wrong missing cases", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].Rationale += ".other" }, "needs an explicit reason"},
		{"no missing cases", func(_ *inventory, m *mapping, _ *releasePolicy) { m.Entries[0].Rationale = " " }, "needs an explicit reason"},
		{"source hash drift", func(_ *inventory, _ *mapping, p *releasePolicy) { p.Entries[0].UpstreamTestHash = hashB }, "hash changed"},
		{"baseline regression", func(_ *inventory, m *mapping, _ *releasePolicy) { m.Entries[1].Disposition = "pending" }, "count decreased"},
		{"unlisted hot path", func(_ *inventory, m *mapping, p *releasePolicy) {
			m.Entries[1].Disposition = "pending"
			p.Entries[1].Tags = []string{"hot-path"}
		}, "remains pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv, m, policy := knownGapReleasePolicy()
			tc.mutate(&inv, &m, &policy)
			if err := validateReleasePolicy(inv, m, policy); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestReleaseKnownGapNeverBypassesStrictMapping(t *testing.T) {
	inv, m, policy := knownGapReleasePolicy()
	_, _, evidence, divergences := baseline()
	root, invPath, mapPath := writeRepo(t, inv, m, evidence, divergences)
	if err := validateReleasePolicy(inv, m, policy); err != nil {
		t.Fatal(err)
	}
	if err := check(invPath, mapPath, "docs/parity/DIVERGENCES.md", root, true); err == nil || !strings.Contains(err.Error(), "remains partial") {
		t.Fatalf("strict mapping must reject deferred partial: %v", err)
	}
}

func TestReleaseKnownGapOutput(t *testing.T) {
	if os.Getenv("PIG_TEST_RELEASE_GAP_OUTPUT") == "1" {
		inv, m, policy := knownGapReleasePolicy()
		if err := validateReleasePolicy(inv, m, policy); err != nil {
			t.Fatal(err)
		}
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestReleaseKnownGapOutput$")
	cmd.Env = append(os.Environ(), "PIG_TEST_RELEASE_GAP_OUTPUT=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gate output: %v\n%s", err, output)
	}
	inv, m, policy := knownGapReleasePolicy()
	count := 0
	for _, entry := range policy.Entries {
		for _, tag := range entry.Tags {
			if tag == "deferred-0.3.x" {
				count++
			}
		}
	}
	for _, want := range []string{inv.Files[0].Path, "remains partial", m.Entries[0].Rationale, "FOLLOWUP-providers", fmt.Sprintf("%d approved 0.3.x known gaps", count), "committed baseline"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("missing %q from gate output:\n%s", want, output)
		}
	}
}

func TestReleaseKnownGapRetainsCommittedBaselineIdentity(t *testing.T) {
	inv, m, policy := knownGapReleasePolicy()
	_, committed, evidence, divergences := baseline()
	root, invPath, mapPath := writeRepo(t, inv, committed, evidence, divergences)
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
	writeJSON(t, mapPath, m)
	policyPath := filepath.Join(root, "policy.json")
	writeJSON(t, policyPath, policy)
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err != nil {
		t.Fatal(err)
	}
	policy.BaselinePorted = nil
	writeJSON(t, policyPath, policy)
	if err := checkReleasePolicy(invPath, mapPath, policyPath, root); err == nil || !strings.Contains(err.Error(), "baseline drops") {
		t.Fatalf("known-gap approval must not lower the committed baseline: %v", err)
	}
}
