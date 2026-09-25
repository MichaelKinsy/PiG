// Command coverage produces a markdown report linking parity scenarios
// to PORT_MAP.md entries.
//
// Usage:
//
//	# 1. Run the suite with JSON output
//	go test -tags=parity ./parity/runner -v -args -pig-parity.results=/tmp/parity.json
//
//	# 2. Generate the report
//	go run ./parity/cmd/coverage \
//	    -results /tmp/parity.json \
//	    -port-map PORT_MAP.md \
//	    -scenarios parity/scenarios > /tmp/coverage.md
//
// The report shows, for every upstream file in PORT_MAP.md:
//   - the port status as recorded in PORT_MAP.md
//   - how many scenarios reference it via `covers = [...]`
//   - whether the last run was green
//
// A `cov:0` row means: file is ported but unverified by the parity suite.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

type scenarioFile struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Covers      []string `toml:"covers"`
	Tags        []string `toml:"tags"`

	// Source is the .toml file path. Populated by loadScenarios, not TOML.
	Source string `toml:"-"`
	// Family is the immediate parent directory under parity/scenarios/,
	// or "_top" for scenarios still at the legacy top level.
	Family string `toml:"-"`
}

type jsonOutcome struct {
	Name      string   `json:"name"`
	Covers    []string `json:"covers"`
	PigMedian int64    `json:"pig_median_ms"`
	PiMedian  int64    `json:"pi_median_ms"`
	Passed    bool     `json:"passed"`
	Failures  []string `json:"failures,omitempty"`
}

type portMapEntry struct {
	UpstreamPath string
	PigPath      string
	Status       string
	LineNum      int
}

func main() {
	results := flag.String("results", "", "JSON results from `go test -pig-parity.results=...`")
	portMap := flag.String("port-map", "PORT_MAP.md", "path to PORT_MAP.md")
	scenariosDir := flag.String("scenarios", "parity/scenarios", "scenarios directory")
	agentsMd := flag.String("agents-md", "", "if set, patch the coverage block in this AGENTS.md in place")
	badge := flag.String("badge", "", "if set, write the behavioral parity coverage badge to this path")
	readme := flag.String("readme", "", "if set, patch the porting block beside the badges in this README in place")
	strict := flag.Bool("strict", false, "exit 1 unless every intended-portable row is complete and behaviorally covered")
	flag.Parse()

	// 1. Discover all scenarios and their covers.
	scenarios, err := loadScenarios(*scenariosDir)
	if err != nil {
		fail("load scenarios: %v", err)
	}

	// 2. Load last-run results (optional).
	var runOutcomes map[string]jsonOutcome
	if *results != "" {
		runOutcomes, err = loadResults(*results, coding.UpstreamVersion)
		if err != nil {
			fail("load results: %v", err)
		}
	}

	// 3. Parse PORT_MAP.md entries.
	entries, err := parsePortMap(*portMap)
	if err != nil {
		fail("parse port map: %v", err)
	}

	// 4. Build coverage index: upstream path -> [scenario names].
	// All scenarios contribute to the full coverage map so reviewers can see
	// which scenarios reference each file. Only behavioral scenarios contribute
	// to the headline verification math. Smoke/registration/boot/deferred
	// scenarios are useful, but they must not certify a file as behaviorally
	// verified (the provider empty-text regression was hidden by exactly that
	// overclaim: --list-models covered provider files without exercising payload
	// conversion or streaming).
	coverage := make(map[string][]string)
	behavioralCoverage := make(map[string][]string)
	for _, sc := range scenarios {
		for _, c := range sc.Covers {
			coverage[c] = append(coverage[c], sc.Name)
			if isBehavioralCoverage(sc, c) {
				behavioralCoverage[c] = append(behavioralCoverage[c], sc.Name)
			}
		}
	}

	units, err := loadUnitEvidence(filepath.Dir(*portMap), entries)
	if err != nil {
		fail("load unit evidence: %v", err)
	}
	addUnitEvidence(coverage, behavioralCoverage, units)

	// 5. Emit the full report.
	emitReport(os.Stdout, entries, coverage, behavioralCoverage, runOutcomes, scenarios)

	// 6. Optionally patch the condensed block into AGENTS.md.
	if *agentsMd != "" {
		if err := patchAgentsMd(*agentsMd, entries, coverage, behavioralCoverage, runOutcomes, scenarios); err != nil {
			fail("patch agents-md: %v", err)
		}
	}

	stats := computeStatusStats(entries, coverage, behavioralCoverage)
	if *badge != "" {
		if err := writeCoverageBadge(*badge, stats); err != nil {
			fail("write coverage badge: %v", err)
		}
	}
	if *readme != "" {
		if err := patchFileBlock(*readme, portingBeginMarker, portingEndMarker, portingBlock(entries, stats, coding.UpstreamVersion)); err != nil {
			fail("patch readme: %v", err)
		}
	}

	// 7. Enforce the foundation gate only when explicitly requested. The normal
	// report remains useful while an upstream sync is in progress.
	if *strict {
		if err := stats.strictError(); err != nil {
			fail("strict: %v", err)
		}
	}

}

func loadScenarios(dir string) ([]scenarioFile, error) {
	var out []scenarioFile
	absRoot, _ := filepath.Abs(dir)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".toml") {
			return nil
		}
		var sc scenarioFile
		if _, err := toml.DecodeFile(path, &sc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		sc.Source = path
		// Family = immediate dir name under scenarios/, or "_top" if the
		// file is at the scenarios root (legacy migration target).
		abs, _ := filepath.Abs(path)
		rel, _ := filepath.Rel(absRoot, abs)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) >= 2 {
			sc.Family = parts[0]
		} else {
			sc.Family = "_top"
		}
		out = append(out, sc)
		return nil
	})
	return out, err
}

type scenarioQualityKind string

const (
	qualityBehavioral       scenarioQualityKind = "behavioral"
	qualityBootOnly         scenarioQualityKind = "boot-only"
	qualityRegistrationOnly scenarioQualityKind = "registration-only"
	qualitySmokeOnly        scenarioQualityKind = "smoke-only"
	qualityDeferred         scenarioQualityKind = "deferred"
)

func scenarioQuality(sc scenarioFile) scenarioQualityKind {
	switch {
	case slices.Contains(sc.Tags, "deferred"):
		return qualityDeferred
	case strings.HasPrefix(sc.Description, "boot-only:") || slices.Contains(sc.Tags, "boot-only"):
		return qualityBootOnly
	case slices.Contains(sc.Tags, "registration-only"):
		return qualityRegistrationOnly
	case slices.Contains(sc.Tags, "smoke-only"):
		return qualitySmokeOnly
	default:
		return qualityBehavioral
	}
}

func isBehavioralCoverage(sc scenarioFile, cover string) bool {
	switch scenarioQuality(sc) {
	case qualityBehavioral:
		return true
	case qualityRegistrationOnly:
		return isRegistrationCoveragePath(cover)
	default:
		return false
	}
}

func isRegistrationCoveragePath(path string) bool {
	switch path {
	case "packages/ai/src/api-registry.ts",
		"packages/ai/src/env-api-keys.ts",
		"packages/ai/src/providers/register-builtins.ts",
		"packages/coding-agent/src/core/auth-storage.ts",
		"packages/coding-agent/src/core/resolve-config-value.ts":
		return true
	default:
		return false
	}
}

func loadResults(path, expectedVersion string) (map[string]jsonOutcome, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var results struct {
		UpstreamVersion string        `json:"upstream_version"`
		Outcomes        []jsonOutcome `json:"outcomes"`
	}
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, err
	}
	if results.UpstreamVersion != expectedVersion {
		return nil, fmt.Errorf("results upstream_version = %q, want %q", results.UpstreamVersion, expectedVersion)
	}
	out := make(map[string]jsonOutcome, len(results.Outcomes))
	for _, o := range results.Outcomes {
		out[o.Name] = o
	}
	return out, nil
}

// PORT_MAP is a minimal markdown table grouped by package:
//
//	## `packages/ai/src/`
//	| upstream | pig | status |
//	|---|---|---|
//	| `packages/ai/src/models.ts` | `ai/registry.go` | ✅ |
//
// We parse only table rows. Everything else is ignored.
func parsePortMap(path string) ([]portMapEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	rowRE := regexp.MustCompile("^\\|\\s+`([^`]+)`\\s+\\|\\s+`([^`]*)`\\s+\\|\\s+(✅|🟡|⬜|⏸|🔴|n/a)\\s+\\|\\s*$")
	var entries []portMapEntry
	for i, line := range strings.Split(string(data), "\n") {
		m := rowRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entries = append(entries, portMapEntry{
			UpstreamPath: m[1],
			PigPath:      m[2],
			Status:       m[3],
			LineNum:      i + 1,
		})
	}
	return entries, nil
}

func emitReport(w *os.File, entries []portMapEntry, coverage, behavioralCoverage map[string][]string, results map[string]jsonOutcome, scenarios []scenarioFile) {
	_, _ = fmt.Fprintln(w, "# PORT_MAP coverage report")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Generated from `%d` PORT_MAP entries and `%d` parity scenarios.\n",
		len(entries), len(scenarios))
	_, _ = fmt.Fprintln(w)

	stats := computeStatusStats(entries, coverage, behavioralCoverage)
	_, _ = fmt.Fprintln(w, stats.summaryLine())
	_, _ = fmt.Fprintln(w, stats.breakdownLine())
	_, _ = fmt.Fprintln(w)

	_, _ = fmt.Fprintln(w, "Behavioral evidence includes paired scenarios and reviewed mutation-proven Go unit tests. Unit tests are listed separately; last run refers only to paired scenarios, not unit execution or exhaustive parity.")
	_, _ = fmt.Fprintln(w)

	// Per-entry table. `scenarios` is every scenario that references the file;
	// `behavioral` is the stricter count used by the verification headline.
	_, _ = fmt.Fprintln(w, "| upstream | port | scenarios | behavioral | last run | unit tests |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|")
	for _, e := range entries {
		scs, unitTests := splitEvidence(matchScenarios(e.UpstreamPath, coverage))
		behavioralScs := matchScenarios(e.UpstreamPath, behavioralCoverage)
		runState := "not run"
		if results != nil {
			runState = aggregateRunState(scs, results)
		}
		covCell := fmt.Sprintf("%d", len(scs))
		if len(scs) > 0 {
			covCell = fmt.Sprintf("%d (%s)", len(scs), strings.Join(scs, ", "))
		}
		behavCell := fmt.Sprintf("%d", len(behavioralScs))
		if len(behavioralScs) > 0 {
			behavCell = fmt.Sprintf("%d (%s)", len(behavioralScs), strings.Join(behavioralScs, ", "))
		}
		if e.Status == "✅" && len(scs) == 0 && len(unitTests) == 0 {
			covCell = "**0 (untested)**"
			behavCell = "**0**"
		} else if e.Status == "✅" && len(scs) > 0 && len(behavioralScs) == 0 {
			behavCell = "**0 (weak-only)**"
		}
		_, _ = fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s | %s |\n",
			e.UpstreamPath, e.Status, covCell, behavCell, runState, strings.Join(unitTests, ", "))
	}
	// Scenarios with no matching PORT_MAP entry (drift detector).
	orphans := findOrphans(scenarios, entries)
	if len(orphans) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "## Orphan covers (scenario references no PORT_MAP entry)")
		_, _ = fmt.Fprintln(w)
		for sc, paths := range orphans {
			for _, p := range paths {
				_, _ = fmt.Fprintf(w, "- `%s` (scenario: %s)\n", p, sc)
			}
		}
	}
}

func matchScenarios(upstream string, coverage map[string][]string) []string {
	out := coverage[upstream]
	sort.Strings(out)
	return out
}

// statusStats is the single source of truth for the verification dashboard.
// All headlines (parity/coverage.md and AGENTS.md COVERAGE block) render from
// the same computed numbers. This prevents drift between reports.
type statusStats struct {
	Total             int // every PORT_MAP row
	Ported            int // ✅: intended-portable surface, done
	Partial           int // 🟡: partial port, documented divergence
	Deferred          int // ⏸: intentional non-port
	Broken            int // 🔴: port exists but broken
	NotStarted        int // ⬜: port not yet attempted
	NA                int // n/a: designed out (TS-only constructs)
	Covered           int // ✅ entries with ≥1 parity scenario of any quality
	Behavioral        int // ✅ entries with ≥1 behavioral scenario (real verification)
	NonBehavioralOnly int // ✅ entries with scenarios but none behavioral (boot/registration/smoke/deferred only)
	Untested          int // ✅ entries with zero parity scenarios
}

func computeStatusStats(entries []portMapEntry, coverage, behavioralCoverage map[string][]string) statusStats {
	var s statusStats
	s.Total = len(entries)
	for _, e := range entries {
		switch e.Status {
		case "✅":
			s.Ported++
			hasAny := len(matchScenarios(e.UpstreamPath, coverage)) > 0
			hasBehavioral := len(matchScenarios(e.UpstreamPath, behavioralCoverage)) > 0
			switch {
			case hasBehavioral:
				s.Covered++
				s.Behavioral++
			case hasAny:
				s.Covered++
				s.NonBehavioralOnly++
			default:
				s.Untested++
			}
		case "🟡":
			s.Partial++
		case "⏸":
			s.Deferred++
		case "🔴":
			s.Broken++
		case "⬜":
			s.NotStarted++
		case "n/a":
			s.NA++
		}
	}
	return s
}

// summaryLine renders the porting headline. Denominator is the intended
// portable surface (✅: everything we mean to port), NOT the raw row count
// (which includes n/a, deferred, partial, etc.).
//
// Verification math distinguishes:
//   - **Behavioral**: ✅ entries with ≥1 behavioral scenario: the honest
//     verification number. This is what gets reported as the headline %.
//   - **Non-behavioral-only**: ✅ entries whose only scenarios are weak
//     quality classes (boot-only, registration-only, smoke-only, or deferred)
//     : referenced but not behaviorally verified. Surfaced separately to
//     prevent metric inflation.
func (s statusStats) strictError() error {
	if s.Partial+s.Broken+s.NotStarted > 0 {
		return fmt.Errorf("%d partial, %d broken, and %d not-started intended-portable rows remain", s.Partial, s.Broken, s.NotStarted)
	}
	if s.NonBehavioralOnly+s.Untested > 0 {
		return fmt.Errorf("%d weak-only and %d untested ported rows remain", s.NonBehavioralOnly, s.Untested)
	}
	return nil
}

func (s statusStats) behavioralPercentage() float64 {
	if s.Ported == 0 {
		return 0
	}
	return float64(s.Behavioral) / float64(s.Ported) * 100
}

func (s statusStats) coverageBadge() string {
	percentage := s.behavioralPercentage()
	color := "#e05d44"
	switch {
	case percentage >= 90:
		color = "#4c1"
	case percentage >= 80:
		color = "#97ca00"
	case percentage >= 70:
		color = "#a4a61d"
	case percentage >= 60:
		color = "#dfb317"
	}
	value := fmt.Sprintf("%.1f%%", percentage)
	label := "behavior tests"
	accessible := html.EscapeString(label + ": " + value)
	return fmt.Sprintf(`<!-- SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP -->
<!-- SPDX-License-Identifier: MIT -->
<svg xmlns="http://www.w3.org/2000/svg" width="154" height="20" role="img" aria-label="%s">
  <title>%s</title>
  <rect width="98" height="20" fill="#555"/>
  <rect x="98" width="56" height="20" fill="%s"/>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
    <text x="49" y="14">behavior tests</text>
    <text x="126" y="14">%s</text>
  </g>
</svg>
`, accessible, accessible, color, html.EscapeString(value))
}

func writeCoverageBadge(path string, stats statusStats) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(stats.coverageBadge()), 0o644)
}

// intendedPortable is the porting denominator: every row PiG means to port.
func (s statusStats) intendedPortable() int {
	return s.Ported + s.Partial + s.Broken + s.NotStarted
}

func (s statusStats) portingPercentage() float64 {
	if s.intendedPortable() == 0 {
		return 100
	}
	return float64(s.Ported) / float64(s.intendedPortable()) * 100
}

func (s statusStats) summaryLine() string {
	pct := s.portingPercentage()
	behavPct := s.behavioralPercentage()
	weakNote := ""
	if s.NonBehavioralOnly > 0 {
		weakNote = fmt.Sprintf(", %d weak-only (no behavioral verification)", s.NonBehavioralOnly)
	}
	return fmt.Sprintf("**Porting:** %d / %d intended-portable entries ✅ (%.1f%%); **Verification:** %d behavioral (%.1f%%)%s, %d untested.",
		s.Ported, s.intendedPortable(), pct,
		s.Behavioral, behavPct, weakNote, s.Untested)
}

// breakdownLine itemizes the non-✅ statuses so readers see why the
// denominator above isn't the raw row count. Empty when nothing is non-✅
// (a clean 100% port with no documented exceptions).
func (s statusStats) breakdownLine() string {
	var parts []string
	if s.NA > 0 {
		parts = append(parts, fmt.Sprintf("%d n/a (designed out)", s.NA))
	}
	if s.Deferred > 0 {
		parts = append(parts, fmt.Sprintf("%d ⏸ deferred", s.Deferred))
	}
	if s.Partial > 0 {
		parts = append(parts, fmt.Sprintf("%d 🟡 partial", s.Partial))
	}
	if s.Broken > 0 {
		parts = append(parts, fmt.Sprintf("%d 🔴 broken", s.Broken))
	}
	if s.NotStarted > 0 {
		parts = append(parts, fmt.Sprintf("%d ⬜ not started", s.NotStarted))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Raw PORT_MAP rows: %d (all ✅).", s.Total)
	}
	return fmt.Sprintf("Raw PORT_MAP rows: %d. Breakdown: %s. See DIVERGENCES.md for the documented exceptions.",
		s.Total, strings.Join(parts, " · "))
}

func aggregateRunState(scs []string, results map[string]jsonOutcome) string {
	if len(scs) == 0 {
		return "not run"
	}
	pass, fail := 0, 0
	for _, n := range scs {
		o, ok := results[n]
		if !ok {
			continue
		}
		if o.Passed {
			pass++
		} else {
			fail++
		}
	}
	if fail > 0 {
		return fmt.Sprintf("%d pass / **%d fail**", pass, fail)
	}
	if pass > 0 {
		return fmt.Sprintf("%d pass", pass)
	}
	return "not run"
}

func findOrphans(scenarios []scenarioFile, entries []portMapEntry) map[string][]string {
	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		known[e.UpstreamPath] = true
	}
	out := make(map[string][]string)
	for _, sc := range scenarios {
		for _, c := range sc.Covers {
			if !known[c] {
				out[sc.Name] = append(out[sc.Name], c)
			}
		}
	}
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "coverage: "+format+"\n", args...)
	os.Exit(1)
}

// ----------------------------------------------------------------------
// AGENTS.md condensed-coverage patcher.
//
// Maintains a machine-generated block between these markers:
//
//	<!-- BEGIN COVERAGE -->
//	...
//	<!-- END COVERAGE -->
//
// Everything inside is rewritten on every `make coverage`. Nothing inside
// is canonical except as a byproduct of the parity suite.
// ----------------------------------------------------------------------

const (
	covBeginMarker = "<!-- BEGIN COVERAGE -->"
	covEndMarker   = "<!-- END COVERAGE -->"
)

type familyRow struct {
	Family     string
	Scenarios  int
	Behavioral int
	BootOnly   int
	Weak       int // registration-only or smoke-only scenarios
	Deferred   int
	Covered    int // distinct upstream files covered by behavioral scenarios
	coveredSet map[string]bool
	Pass       int // scenarios whose last run passed
	Fail       int // scenarios whose last run failed
	NotRun     int
}

func patchAgentsMd(path string, entries []portMapEntry, coverage, behavioralCoverage map[string][]string, results map[string]jsonOutcome, scenarios []scenarioFile) error {
	block := buildCoverageBlock(entries, coverage, behavioralCoverage, results, scenarios)
	return patchFileBlock(path, covBeginMarker, covEndMarker, block)
}

func buildCoverageBlock(entries []portMapEntry, coverage, behavioralCoverage map[string][]string, results map[string]jsonOutcome, scenarios []scenarioFile) string {
	// 1. Top-line numbers.
	stats := computeStatusStats(entries, coverage, behavioralCoverage)

	// Count weak/non-behavioral scenarios for quality visibility.
	var bootOnly, registrationOnly, smokeOnly int
	for _, sc := range scenarios {
		switch scenarioQuality(sc) {
		case qualityBootOnly:
			bootOnly++
		case qualityRegistrationOnly:
			registrationOnly++
		case qualitySmokeOnly:
			smokeOnly++
		}
	}

	// 2. Per-family aggregation.
	famIdx := make(map[string]*familyRow)
	for _, sc := range scenarios {
		f := sc.Family
		if f == "" {
			f = "_top"
		}
		row, ok := famIdx[f]
		if !ok {
			row = &familyRow{Family: f, coveredSet: map[string]bool{}}
			famIdx[f] = row
		}
		quality := scenarioQuality(sc)
		if quality == qualityDeferred {
			row.Deferred++
			continue // deferred scenarios don't count as coverage
		}
		row.Scenarios++
		switch quality {
		case qualityBehavioral:
			row.Behavioral++
		case qualityBootOnly:
			row.BootOnly++
		default:
			row.Weak++
		}
		seen := map[string]bool{}
		for _, c := range sc.Covers {
			if !isBehavioralCoverage(sc, c) || seen[c] || row.coveredSet[c] {
				continue
			}
			seen[c] = true
			row.coveredSet[c] = true
			row.Covered++
		}
		if o, ok := results[sc.Name]; ok {
			if o.Passed {
				row.Pass++
			} else {
				row.Fail++
			}
		} else {
			row.NotRun++
		}
	}

	families := make([]string, 0, len(famIdx))
	for f := range famIdx {
		families = append(families, f)
	}
	sort.Strings(families)

	// 3. Render.
	var b strings.Builder
	fmt.Fprintln(&b, "<!--")
	fmt.Fprintln(&b, "  Machine-generated by `make coverage`. Do not hand-edit.")
	fmt.Fprintln(&b, "  This block is the ONLY status claim AGENTS.md makes about the")
	fmt.Fprintln(&b, "  port. Everything else is invariant rule, not progress narrative.")
	fmt.Fprintln(&b, "-->")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, stats.summaryLine())
	fmt.Fprintln(&b, stats.breakdownLine())
	fmt.Fprintln(&b, "Behavioral evidence includes paired scenarios and reviewed mutation-proven unit tests; the family table below counts paired scenarios only.")
	if bootOnly > 0 || registrationOnly > 0 || smokeOnly > 0 {
		fmt.Fprintf(&b, "Weak scenarios not counted as behavioral verification: %d boot-only, %d registration-only, %d smoke-only.\n", bootOnly, registrationOnly, smokeOnly)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "| family | scenarios | behavioral | boot-only | weak | deferred | upstream behavioral covered | last run |")
	fmt.Fprintln(&b, "|---|---:|---:|---:|---:|---:|---:|---|")
	for _, f := range families {
		row := famIdx[f]
		lastRun := "not run"
		switch {
		case row.Fail > 0:
			lastRun = fmt.Sprintf("%d pass / **%d fail**", row.Pass, row.Fail)
		case row.Pass > 0 && row.NotRun == 0:
			lastRun = fmt.Sprintf("%d pass", row.Pass)
		case row.Pass > 0:
			lastRun = fmt.Sprintf("%d pass / %d not run", row.Pass, row.NotRun)
		case row.Scenarios > 0:
			lastRun = "not run"
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %d | %d | %s |\n",
			row.Family, row.Scenarios, row.Behavioral, row.BootOnly, row.Weak, row.Deferred, row.Covered, lastRun)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Full per-file detail: `parity/coverage.md`.")
	return b.String()
}
