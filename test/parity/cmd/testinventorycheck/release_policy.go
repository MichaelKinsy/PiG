package main

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// checkReleasePolicy enforces the release policy. publicMainRef names the local ref that holds public main; the committed baseline must be its ancestor.
func checkReleasePolicy(inventoryPath, mappingPath, policyPath, repoRoot, publicMainRef string) error {
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
	if err := verifyCommittedBaseline(repoRoot, mappingPath, publicMainRef, policy, m); err != nil {
		return err
	}
	return validateReleasePolicy(inv, m, policy)
}

func verifyCommittedBaseline(repoRoot, mappingPath, publicMainRef string, policy releasePolicy, current mapping) error {
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
	if err := requireReachableFromPublicMain(root, policy.BaselineCommit, publicMainRef); err != nil {
		return err
	}
	anchor, carried, err := baselineMappingPath(strings.Fields(string(tree)), filepath.Base(rel), policy.UpstreamVersion)
	if err != nil {
		return fmt.Errorf("committed test baseline %s: %w", policy.BaselineCommit, err)
	}
	data, err := exec.CommandContext(context.Background(), "git", "-C", root, "show", policy.BaselineCommit+":"+anchor).Output()
	if err != nil {
		return fmt.Errorf("read committed test baseline %s (fetch this commit; do not lower the baseline): %w", policy.BaselineCommit, err)
	}
	var committed mapping
	if err := json.Unmarshal(data, &committed); err != nil {
		return fmt.Errorf("decode committed test baseline: %w", err)
	}
	wantVersion := policy.UpstreamVersion
	if carried {
		wantVersion = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(anchor), "test-mapping-v"), ".json")
	}
	if committed.UpstreamVersion != wantVersion {
		return fmt.Errorf("committed test baseline %s maps upstream %s, not %s", policy.BaselineCommit, committed.UpstreamVersion, wantVersion)
	}
	// The committed mapping is a floor: the stored baseline must keep every path it ported. The stored baseline may extend past the anchor with known paths ported since, which only raises the count validateReleasePolicy enforces; a release squashed onto a public commit that predates its own ports therefore keeps its full baseline.
	// An anchor that predates the policy's upstream version (the first release of a leap) carries the previous version's floor: each path it ported that the current denominator still has must stay ported. Paths upstream removed are not part of the floor.
	inCurrent := make(map[string]bool, len(current.Entries))
	for _, entry := range current.Entries {
		inCurrent[entry.Path] = true
	}
	var dropped []string
	for _, entry := range committed.Entries {
		if entry.Disposition == "ported" && (!carried || inCurrent[entry.Path]) && !slices.Contains(policy.BaselinePorted, entry.Path) {
			dropped = append(dropped, entry.Path)
		}
	}
	if len(dropped) > 0 {
		slices.Sort(dropped)
		return fmt.Errorf("release policy baseline drops %d ported paths recorded by committed mapping %s, first %q", len(dropped), policy.BaselineCommit, dropped[0])
	}
	return nil
}

// defaultPublicMainRef is the remote-tracking ref of public main in a clone of it, as CI checks it out. The release lands as one squash on public main, so the baseline anchor must be a commit every clone of it can fetch. The name is fully qualified because Git's short-name lookup prefers a local tag or branch named origin/main over the remote-tracking ref. A clone that names the public remote differently passes its own remote-tracking ref with -public-main-ref.
const defaultPublicMainRef = "refs/remotes/origin/main"

// requireReachableFromPublicMain rejects an anchor that a local clone can read but public main does not contain, such as a staging-only commit or a force-pushed pull-request head.
func requireReachableFromPublicMain(root, commit, publicMainRef string) error {
	if publicMainRef == "" || strings.HasPrefix(publicMainRef, "-") {
		return fmt.Errorf("public main ref %q must name a Git revision", publicMainRef)
	}
	output, err := exec.CommandContext(context.Background(), "git", "-C", root, "merge-base", "--is-ancestor", commit, publicMainRef).CombinedOutput()
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return fmt.Errorf("committed test baseline %s is not reachable from %s; anchor it on a public main commit (do not lower the baseline)", commit, publicMainRef)
	}
	return fmt.Errorf("check committed test baseline %s against %s (fetch public main into it, or name the ref that holds it with make PUBLIC_MAIN_REF=... or -public-main-ref): %w: %s", commit, publicMainRef, err, strings.TrimSpace(string(output)))
}

// baselineMappingPath picks the mapping inside the anchor commit's tree that is the committed floor. It is the policy version's own mapping; when the anchor predates that version, it is the newest mapping of an older upstream version, and carried is true. A mapping of a newer version is never a floor.
func baselineMappingPath(tree []string, mappingName, version string) (anchor string, carried bool, err error) {
	var exact []string
	best := ""
	bestCount := 0
	var bestVersion []int
	for _, name := range tree {
		base := path.Base(name)
		if base == mappingName {
			exact = append(exact, name)
			continue
		}
		found := versionedMappingName.FindStringSubmatch(base)
		if found == nil {
			continue
		}
		if compareVersions(parseVersion(found[1]), parseVersion(version)) >= 0 {
			continue
		}
		switch order := compareVersions(parseVersion(found[1]), bestVersion); {
		case best == "" || order > 0:
			best, bestVersion, bestCount = name, parseVersion(found[1]), 1
		case order == 0:
			bestCount++
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], false, nil
	case len(exact) > 1:
		return "", false, fmt.Errorf("must contain one unambiguous %s mapping; found %d", mappingName, len(exact))
	case bestCount > 1:
		return "", false, fmt.Errorf("must contain one unambiguous mapping for the newest older upstream version; found %d of %s", bestCount, path.Base(best))
	case best != "":
		return best, true, nil
	}
	return "", false, fmt.Errorf("must contain one unambiguous %s mapping, or one for an older upstream version; found none", mappingName)
}

var versionedMappingName = regexp.MustCompile(`^test-mapping-v(\d+(?:\.\d+)*)\.json$`)

func parseVersion(v string) []int {
	var parts []int
	for part := range strings.SplitSeq(v, ".") {
		n, _ := strconv.Atoi(part)
		parts = append(parts, n)
	}
	return parts
}

func compareVersions(a, b []int) int {
	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return cmp.Compare(x, y)
		}
	}
	return 0
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
