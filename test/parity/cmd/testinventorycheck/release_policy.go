package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// releasePolicy keeps reviewed production-path tags separate from the generated test denominator.
type releasePolicy struct {
	UpstreamVersion string        `json:"upstreamVersion"`
	BaselineCommit  string        `json:"baselineCommit"`
	BaselinePorted  []string      `json:"baselinePorted"`
	Entries         []policyEntry `json:"entries"`
}

// policyAreas is the closed set of release-policy areas, in report order.
var policyAreas = []string{"tools", "providers", "sessions", "extensions", "tui", "cli", "utilities"}

type policyEntry struct {
	Path             string   `json:"path"`
	UpstreamTestHash string   `json:"upstreamTestHash"`
	Area             string   `json:"area"`
	Tags             []string `json:"tags"`
	Rationale        string   `json:"rationale"`
	// DesignedOutCases is the owner-approved set of upstream case ids designed out inside a ported or partial file. It is the denominator the mapping's designedOutCases must equal, so neither file can widen or drop the exception alone.
	DesignedOutCases []string `json:"designedOutCases,omitempty"`
}

func checkReleasePolicy(inventoryPath, mappingPath, policyPath, repoRoot string) error {
	var inv inventory
	if err := decodeJSON(inventoryPath, &inv); err != nil {
		return err
	}
	var m mapping
	if err := decodeJSON(mappingPath, &m); err != nil {
		return err
	}
	var policy releasePolicy
	if err := decodeJSON(policyPath, &policy); err != nil {
		return err
	}
	if err := verifyCommittedBaseline(repoRoot, mappingPath, policy); err != nil {
		return err
	}
	return validateReleasePolicy(inv, m, policy)
}

func verifyCommittedBaseline(repoRoot, mappingPath string, policy releasePolicy) error {
	if _, err := hex.DecodeString(policy.BaselineCommit); err != nil || len(policy.BaselineCommit) != 40 {
		return fmt.Errorf("release policy baselineCommit must be a full Git commit hash")
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	path, err := filepath.Abs(mappingPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("release mapping must be inside repository root")
	}
	tree, err := exec.CommandContext(context.Background(), "git", "-C", root, "ls-tree", "-r", "--name-only", policy.BaselineCommit).Output()
	if err != nil {
		return fmt.Errorf("read committed test baseline %s (fetch this commit; do not lower the baseline): %w", policy.BaselineCommit, err)
	}
	var candidates []string
	for name := range strings.SplitSeq(strings.TrimSpace(string(tree)), "\n") {
		if filepath.Base(name) == filepath.Base(rel) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) != 1 {
		return fmt.Errorf("committed test baseline %s must contain one unambiguous %s mapping; found %d", policy.BaselineCommit, filepath.Base(rel), len(candidates))
	}
	data, err := exec.CommandContext(context.Background(), "git", "-C", root, "show", policy.BaselineCommit+":"+candidates[0]).Output()
	if err != nil {
		return fmt.Errorf("read committed test baseline %s (fetch this commit; do not lower the baseline): %w", policy.BaselineCommit, err)
	}
	var committed mapping
	if err := json.Unmarshal(data, &committed); err != nil {
		return fmt.Errorf("decode committed test baseline: %w", err)
	}
	if committed.UpstreamVersion != policy.UpstreamVersion {
		return fmt.Errorf("committed test baseline %s maps upstream %s, not %s", policy.BaselineCommit, committed.UpstreamVersion, policy.UpstreamVersion)
	}
	// The committed mapping is a floor: the stored baseline must keep every path it ported. The stored baseline may extend past the anchor with known paths ported since, which only raises the count validateReleasePolicy enforces; a release squashed onto a public commit that predates its own ports therefore keeps its full baseline.
	var dropped []string
	for _, entry := range committed.Entries {
		if entry.Disposition == "ported" && !slices.Contains(policy.BaselinePorted, entry.Path) {
			dropped = append(dropped, entry.Path)
		}
	}
	if len(dropped) > 0 {
		slices.Sort(dropped)
		return fmt.Errorf("release policy baseline drops %d ported paths recorded by committed mapping %s, first %q", len(dropped), policy.BaselineCommit, dropped[0])
	}
	return nil
}

func validateReleasePolicy(inv inventory, m mapping, policy releasePolicy) error {
	if policy.UpstreamVersion != inv.UpstreamVersion || policy.UpstreamVersion != m.UpstreamVersion {
		return fmt.Errorf("release policy upstream version does not match inventory and mapping")
	}
	if len(policy.BaselineCommit) != 40 {
		return fmt.Errorf("release policy needs the full committed baseline identity")
	}
	files := make(map[string]inventoryFile, len(inv.Files))
	for _, file := range inv.Files {
		files[file.Path] = file
	}
	if !slices.IsSorted(policy.BaselinePorted) || !unique(policy.BaselinePorted) {
		return fmt.Errorf("release policy baseline paths must be sorted and unique")
	}
	for _, path := range policy.BaselinePorted {
		if _, ok := files[path]; !ok {
			return fmt.Errorf("release policy baseline names unknown test %q", path)
		}
	}
	mapped := make(map[string]mappingEntry, len(m.Entries))
	for _, entry := range m.Entries {
		mapped[entry.Path] = entry
	}
	ported := dispositionCounts(m)["ported"]
	var findings []error
	if ported < len(policy.BaselinePorted) {
		findings = append(findings, fmt.Errorf("ported test-file count decreased: %d < committed baseline %d (%s)", ported, len(policy.BaselinePorted), policy.BaselineCommit))
	}
	knownGaps := 0
	seen := make(map[string]bool, len(policy.Entries))
	previous := ""
	for _, entry := range policy.Entries {
		if entry.Path == "" || seen[entry.Path] || entry.Path < previous {
			return fmt.Errorf("release policy paths must be nonempty, sorted and unique at %q", entry.Path)
		}
		previous = entry.Path
		seen[entry.Path] = true
		file, ok := files[entry.Path]
		if !ok {
			return fmt.Errorf("release policy names unknown test %q", entry.Path)
		}
		if entry.UpstreamTestHash != file.SHA256 {
			return fmt.Errorf("release policy hash changed for %q; review its production-path tags", entry.Path)
		}
		if !slices.Contains(policyAreas, entry.Area) {
			return fmt.Errorf("release policy %q has unknown area %q", entry.Path, entry.Area)
		}
		if entry.Rationale == "" || !unique(entry.Tags) {
			return fmt.Errorf("release policy %q needs a rationale and unique tags", entry.Path)
		}
		for _, tag := range entry.Tags {
			if tag != "hot-path" && tag != "deferred-0.3.x" {
				return fmt.Errorf("release policy %q has unknown tag %q", entry.Path, tag)
			}
		}
		disposition, ok := mapped[entry.Path]
		if !ok {
			return fmt.Errorf("release policy %q has no test mapping", entry.Path)
		}
		if err := validateDesignedOutApproval(entry, disposition); err != nil {
			return err
		}
		openHotPath := slices.Contains(entry.Tags, "hot-path") && (disposition.Disposition == "pending" || disposition.Disposition == "partial")
		if slices.Contains(entry.Tags, "deferred-0.3.x") {
			if err := validateKnownGap(policy.UpstreamVersion, entry, disposition, openHotPath); err != nil {
				findings = append(findings, err)
				continue
			}
			knownGaps++
			fmt.Printf("0.3.x known gap [%s]: %s remains %s (%d upstream case sites)\n  %s\n  Missing cases: %s\n", entry.Area, entry.Path, disposition.Disposition, file.CaseCount, entry.Rationale, disposition.Rationale)
		} else if openHotPath {
			findings = append(findings, fmt.Errorf("hot-path %s: %s remains %s (%d upstream case sites): %s", entry.Area, entry.Path, disposition.Disposition, file.CaseCount, disposition.Rationale))
		}
	}
	for _, file := range inv.Files {
		if !seen[file.Path] {
			findings = append(findings, fmt.Errorf("release policy missing production-path review for %q", file.Path))
		}
	}
	fmt.Printf("upstream test release gate: %d approved 0.3.x known gaps (not ported; test assertions remain enforced)\n", knownGaps)
	if err := errors.Join(findings...); err != nil {
		return fmt.Errorf("upstream test release gate blocked (%d findings):\n%w", len(findings), err)
	}
	fmt.Printf("upstream test release gate: OK (%d reviewed paths, %d ported; committed baseline %d)\n", len(policy.Entries), ported, len(policy.BaselinePorted))
	return nil
}

// validateDesignedOutApproval requires the mapping's designedOutCases and the reviewed policy's approved list to name the same cases, and requires owner approval whenever any case is designed out.
func validateDesignedOutApproval(entry policyEntry, disposition mappingEntry) error {
	if !unique(entry.DesignedOutCases) {
		return fmt.Errorf("release policy %q has duplicate designedOutCases", entry.Path)
	}
	if len(entry.DesignedOutCases) > 0 && !strings.Contains(entry.Rationale, "SCRUTINIZED:approved") {
		return fmt.Errorf("release policy %q lists designedOutCases and needs SCRUTINIZED:approved owner approval in its rationale", entry.Path)
	}
	if !slices.Equal(slices.Sorted(slices.Values(entry.DesignedOutCases)), slices.Sorted(slices.Values(disposition.DesignedOutCases))) {
		return fmt.Errorf("release policy %q: mapping designedOutCases %q differ from the approved policy list %q; approve the change in both files", entry.Path, disposition.DesignedOutCases, entry.DesignedOutCases)
	}
	return nil
}
