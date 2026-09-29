// Command testinventorycheck validates the reviewed disposition mapping over the
// upstream-test inventory. It is the test-level analogue of interfaceinventory /
// behaviorcheck: the inventory (extract-test-inventory.mjs) is the mechanical
// denominator of upstream test obligations, and this gate proves every obligation
// carries a reviewed disposition whose evidence still resolves.
//
// Closure enforced:
//   - every inventory file has exactly one mapping entry, and every entry names a
//     real inventory file (no orphans, no gaps);
//   - each entry's upstreamTestHash equals the inventory content hash, so an
//     upstream leap that edits a test invalidates its disposition and forces
//     re-review;
//   - ported/partial/scenario-covered dispositions name evidence that exists and
//     references the upstream test (path#fragment), a divergence names a D<N> that
//     appears in docs/parity/DIVERGENCES.md, designed-out names a rationale and any
//     evidence it cites resolves too;
//   - a ported/partial file may list designedOutCases (exact inventory case ids) when
//     only some cases are designed out; the rest stay covered by verified evidence;
//     ids shared by two case sites are rejected, and -release-policy requires the reviewed
//     policy row to list the same approved ids;
//   - every non-designed-out row citing TestNodeVendoredTuiUpstreamTests is registered
//     in that harness, and no registered file is designed-out or unmapped;
//   - with -strict, no obligation may remain pending or partial;
//   - with -release-policy and -write-known-gaps or -check-known-gaps, the marked
//     current-state block of a Markdown file is regenerated or compared;
//   - with -release-policy, every upstream test has a reviewed production-path tag,
//     hot-path obligations are closed or explicitly recorded as approved 0.3.x known gaps,
//     and the ported total does not decrease from the verified committed baseline.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
)

type inventory struct {
	UpstreamVersion      string          `json:"upstreamVersion"`
	Generator            string          `json:"generator"`
	FileCount            int             `json:"fileCount"`
	CaseCount            int             `json:"caseCount"`
	DynamicCaseSiteCount int             `json:"dynamicCaseSiteCount"`
	Files                []inventoryFile `json:"files"`
}

type inventoryFile struct {
	Path             string `json:"path"`
	Package          string `json:"package"`
	SHA256           string `json:"sha256"`
	CaseCount        int    `json:"caseCount"`
	DynamicCaseSites int    `json:"dynamicCaseSites,omitempty"`
	Cases            []struct {
		ID        string   `json:"id"`
		Kind      string   `json:"kind"`
		Modifiers []string `json:"modifiers"`
		Line      int      `json:"line"`
	} `json:"cases"`
}

type mapping struct {
	UpstreamVersion string         `json:"upstreamVersion"`
	Entries         []mappingEntry `json:"entries"`
}

type mappingEntry struct {
	Path             string   `json:"path"`
	Disposition      string   `json:"disposition"`
	UpstreamTestHash string   `json:"upstreamTestHash"`
	Evidence         []string `json:"evidence,omitempty"`
	Divergence       string   `json:"divergence,omitempty"`
	Rationale        string   `json:"rationale,omitempty"`
	// DesignedOutCases names upstream case IDs (exact inventory ids) that are
	// designed out inside a ported or partial file. The remaining cases are
	// covered by the entry's verified evidence; the file itself is not designed out.
	DesignedOutCases []string `json:"designedOutCases,omitempty"`
}

var (
	hashPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	divergencePattern = regexp.MustCompile(`^D[0-9]+$`)
	validDispositions = []string{"ported", "partial", "scenario-covered", "designed-out", "divergence", "pending"}
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "test inventory:", err)
		os.Exit(1)
	}
}

// run executes one gate invocation. The Node-bridge harness check runs before check so a failing invocation never prints check's OK summary.
func run(args []string) error {
	flags := flag.NewFlagSet("testinventorycheck", flag.ExitOnError)
	inventoryPath := flags.String("inventory", "test/parity/interfaces/upstream-tests-v"+coding.UpstreamVersion+".json", "generated upstream-test inventory")
	mappingPath := flags.String("mapping", "test/parity/interfaces/test-mapping-v"+coding.UpstreamVersion+".json", "reviewed disposition mapping")
	divergencesPath := flags.String("divergences", "docs/parity/DIVERGENCES.md", "divergence ledger")
	repoRoot := flags.String("repo-root", ".", "root for resolving evidence references")
	strict := flags.Bool("strict", false, "reject pending and partial dispositions")
	releasePolicyPath := flags.String("release-policy", "", "reviewed hot-path tags and committed ported baseline; enforce release closure")
	writeKnownGaps := flags.String("write-known-gaps", "", "with -release-policy, regenerate the marked known-gaps block in this Markdown file")
	checkKnownGaps := flags.String("check-known-gaps", "", "with -release-policy, fail when the marked known-gaps block in this Markdown file is stale")
	generatePending := flags.Bool("generate-pending", false, "write an all-pending mapping for the inventory to stdout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*writeKnownGaps != "" || *checkKnownGaps != "") && *releasePolicyPath == "" {
		return fmt.Errorf("-write-known-gaps and -check-known-gaps require -release-policy")
	}
	if *generatePending {
		return writePendingMapping(*inventoryPath)
	}
	if err := checkNodeBridgeHarness(*mappingPath, *repoRoot); err != nil {
		return err
	}
	if err := check(*inventoryPath, *mappingPath, *divergencesPath, *repoRoot, *strict); err != nil {
		return err
	}
	if *releasePolicyPath != "" {
		if err := checkReleasePolicy(*inventoryPath, *mappingPath, *releasePolicyPath, *repoRoot); err != nil {
			return err
		}
		return syncKnownGapsDoc(*inventoryPath, *mappingPath, *releasePolicyPath, *writeKnownGaps, *checkKnownGaps)
	}
	return nil
}

// writePendingMapping prints one pending entry per inventory file, in inventory
// order, bound to the file's current content hash.
func writePendingMapping(inventoryPath string) error {
	var inv inventory
	if err := decodeJSON(inventoryPath, &inv); err != nil {
		return err
	}
	if inv.UpstreamVersion != coding.UpstreamVersion {
		return fmt.Errorf("inventory upstreamVersion = %q, want %q", inv.UpstreamVersion, coding.UpstreamVersion)
	}
	m := mapping{UpstreamVersion: inv.UpstreamVersion, Entries: make([]mappingEntry, 0, len(inv.Files))}
	for _, file := range inv.Files {
		m.Entries = append(m.Entries, mappingEntry{Path: file.Path, Disposition: "pending", UpstreamTestHash: file.SHA256})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(m)
}

func check(inventoryPath, mappingPath, divergencesPath, repoRoot string, strict bool) error {
	var inv inventory
	if err := decodeJSON(inventoryPath, &inv); err != nil {
		return err
	}
	var m mapping
	if err := decodeJSON(mappingPath, &m); err != nil {
		return err
	}
	if inv.UpstreamVersion != coding.UpstreamVersion {
		return fmt.Errorf("inventory upstreamVersion = %q, want %q", inv.UpstreamVersion, coding.UpstreamVersion)
	}
	if m.UpstreamVersion != coding.UpstreamVersion {
		return fmt.Errorf("mapping upstreamVersion = %q, want %q", m.UpstreamVersion, coding.UpstreamVersion)
	}

	files := make(map[string]inventoryFile, len(inv.Files))
	previousPath := ""
	for _, file := range inv.Files {
		if file.Path == "" || files[file.Path].Path != "" {
			return fmt.Errorf("inventory has empty or duplicate path %q", file.Path)
		}
		if previousPath != "" && file.Path < previousPath {
			return fmt.Errorf("inventory is not sorted at %q", file.Path)
		}
		if !hashPattern.MatchString(file.SHA256) {
			return fmt.Errorf("inventory file %q has invalid hash %q", file.Path, file.SHA256)
		}
		previousPath = file.Path
		files[file.Path] = file
	}

	divergences, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(divergencesPath)))
	if err != nil {
		return fmt.Errorf("read divergences: %w", err)
	}

	mapped := make(map[string]bool, len(m.Entries))
	previousPath = ""
	for _, entry := range m.Entries {
		if mapped[entry.Path] {
			return fmt.Errorf("mapping has duplicate path %q", entry.Path)
		}
		if previousPath != "" && entry.Path < previousPath {
			return fmt.Errorf("mapping is not sorted at %q", entry.Path)
		}
		previousPath = entry.Path
		mapped[entry.Path] = true

		file, exists := files[entry.Path]
		if !exists {
			return fmt.Errorf("mapping entry %q names no inventory file", entry.Path)
		}
		if entry.UpstreamTestHash != file.SHA256 {
			return fmt.Errorf("mapping entry %q hash %q does not match inventory %q (upstream test changed; re-review)", entry.Path, entry.UpstreamTestHash, file.SHA256)
		}
		if !slices.Contains(validDispositions, entry.Disposition) {
			return fmt.Errorf("mapping entry %q has unsupported disposition %q", entry.Path, entry.Disposition)
		}
		if !unique(entry.Evidence) {
			return fmt.Errorf("mapping entry %q has duplicate evidence", entry.Path)
		}

		if err := checkDisposition(repoRoot, entry, file, string(divergences)); err != nil {
			return err
		}
		if strict && (entry.Disposition == "pending" || entry.Disposition == "partial") {
			return fmt.Errorf("mapping entry %q remains %s", entry.Path, entry.Disposition)
		}
	}
	for path := range files {
		if !mapped[path] {
			return fmt.Errorf("inventory file %q has no reviewed disposition", path)
		}
	}

	counts := dispositionCounts(m)
	fmt.Printf("test inventory: OK (%d files: %d ported, %d partial, %d scenario-covered, %d designed-out, %d divergence, %d pending; upstream %s)\n",
		len(inv.Files), counts["ported"], counts["partial"], counts["scenario-covered"], counts["designed-out"], counts["divergence"], counts["pending"], inv.UpstreamVersion)
	return nil
}

func checkDisposition(repoRoot string, entry mappingEntry, file inventoryFile, divergences string) error {
	if err := checkDesignedOutCases(entry, file); err != nil {
		return err
	}
	switch entry.Disposition {
	case "ported", "partial":
		if len(entry.Evidence) == 0 {
			return fmt.Errorf("%s entry %q needs evidence", entry.Disposition, entry.Path)
		}
		if !hasSuffix(entry.Evidence, "_test.go") {
			return fmt.Errorf("%s entry %q needs at least one Go test in evidence", entry.Disposition, entry.Path)
		}
		if entry.Disposition == "partial" && entry.Rationale == "" {
			return fmt.Errorf("partial entry %q needs a rationale for the uncovered cases", entry.Path)
		}
		return checkReferences(repoRoot, entry.Path, entry.Evidence)
	case "scenario-covered":
		if len(entry.Evidence) == 0 || !containsScenario(entry.Evidence) {
			return fmt.Errorf("scenario-covered entry %q needs a parity scenario in evidence", entry.Path)
		}
		return checkReferences(repoRoot, entry.Path, entry.Evidence)
	case "designed-out":
		if entry.Rationale == "" {
			return fmt.Errorf("designed-out entry %q needs a rationale", entry.Path)
		}
		// Evidence cited by a designed-out file (the behavioral counterpart that
		// still exercises shipped code) must resolve like any other evidence.
		return checkReferences(repoRoot, entry.Path, entry.Evidence)
	case "divergence":
		if !divergencePattern.MatchString(entry.Divergence) {
			return fmt.Errorf("divergence entry %q needs a D<N> divergence id", entry.Path)
		}
		if !strings.Contains(divergences, entry.Divergence) {
			return fmt.Errorf("divergence entry %q names %s, absent from the divergence ledger", entry.Path, entry.Divergence)
		}
	}
	return nil
}

// checkDesignedOutCases validates a mixed closure: a ported or partial file in
// which named upstream cases are designed out while the others stay covered by
// verified evidence. Each id must be a distinct inventory case, the set must not
// be the whole file (that is a designed-out entry), and it needs a rationale. An id shared by two case sites is rejected because the mapping cannot say which site is exempt.
func checkDesignedOutCases(entry mappingEntry, file inventoryFile) error {
	if len(entry.DesignedOutCases) == 0 {
		return nil
	}
	if entry.Disposition != "ported" && entry.Disposition != "partial" {
		return fmt.Errorf("%s entry %q lists designedOutCases; only ported or partial entries may design out individual cases", entry.Disposition, entry.Path)
	}
	if entry.Rationale == "" {
		return fmt.Errorf("entry %q lists designedOutCases and needs a rationale for them", entry.Path)
	}
	if !unique(entry.DesignedOutCases) {
		return fmt.Errorf("entry %q has duplicate designedOutCases", entry.Path)
	}
	inventorySites := make(map[string]int, len(file.Cases))
	for _, c := range file.Cases {
		inventorySites[c.ID]++
	}
	for _, id := range entry.DesignedOutCases {
		switch sites := inventorySites[id]; {
		case sites == 0:
			return fmt.Errorf("entry %q designedOutCases names %q, which is not an upstream case of the file", entry.Path, id)
		case sites > 1:
			return fmt.Errorf("entry %q designedOutCases names %q, which is ambiguous: %d upstream case sites share the id", entry.Path, id, sites)
		}
	}
	if len(entry.DesignedOutCases) >= len(inventorySites) {
		return fmt.Errorf("entry %q designs out every upstream case; classify the file designed-out", entry.Path)
	}
	return nil
}

// checkReferences requires each evidence file to exist and, when a #fragment is
// present, to contain that fragment (the upstream test path the Go test mirrors).
func checkReferences(root, id string, references []string) error {
	for _, reference := range references {
		path, fragment, _ := strings.Cut(reference, "#")
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return fmt.Errorf("entry %q evidence %q: %w", id, reference, err)
		}
		if fragment != "" && !strings.Contains(string(data), fragment) {
			return fmt.Errorf("entry %q evidence %q has no matching fragment", id, reference)
		}
	}
	return nil
}

func containsScenario(references []string) bool {
	for _, reference := range references {
		path, _, _ := strings.Cut(reference, "#")
		if strings.HasPrefix(path, "test/parity/scenarios/") && strings.HasSuffix(path, ".toml") {
			return true
		}
	}
	return false
}

func hasSuffix(references []string, suffix string) bool {
	for _, reference := range references {
		path, _, _ := strings.Cut(reference, "#")
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func unique(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func decodeJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
