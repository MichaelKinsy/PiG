// Command portreconcile produces the honest gap ledger for the Pig port. It
// joins the compiler-derived upstream interface inventory to the human-ratified
// PORT_MAP dispositions and to the on-disk Go tree, classifying every top-level
// upstream declaration as audited-ported, implemented-unaudited (mapped Go code
// exists but the parity audit is not recorded), designed-out, partial/deferred,
// or a genuine gap (mapped Go target absent). Member declarations roll up to
// their parent's file, so the ledger measures missing behavior at file
// granularity rather than manufacturing one obligation per struct field.
//
// The command exits non-zero when any declaration is a genuine gap, so CI can
// assert that the reconciled missing-file count stays zero.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

type inventory struct {
	Interfaces []declaration `json:"interfaces"`
}

type declaration struct {
	ID       string `json:"id"`
	Package  string `json:"package"`
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
	Source   struct {
		Path string `json:"path"`
	} `json:"source"`
}

type portMapRow struct {
	target      string
	disposition string
}

type classification string

const (
	classPortedAudited        classification = "ported-audited"
	classImplementedUnaudited classification = "implemented-unaudited"
	classImplementedCovered   classification = "implemented-covered"
	classImplementedUncovered classification = "implemented-uncovered"
	classDesignedOut          classification = "designed-out"
	classPartialDeferred      classification = "partial-deferred"
	classUnjoined             classification = "unjoined"
	classGap                  classification = "gap-candidate"
)

// classOrder fixes the report row order so output is deterministic.
var classOrder = []classification{
	classPortedAudited, classImplementedCovered, classImplementedUnaudited,
	classImplementedUncovered, classDesignedOut, classPartialDeferred,
	classUnjoined, classGap,
}

// packageSourceRoots maps each upstream npm package (as it appears in the
// inventory's package field) to its upstream monorepo source root, which is the
// path space PORT_MAP keys on. These are stable for a pinned upstream version.
var packageSourceRoots = map[string]string{
	"@earendil-works/pi-agent-core":   "packages/agent/src",
	"@earendil-works/pi-ai":           "packages/ai/src",
	"@earendil-works/pi-coding-agent": "packages/coding-agent/src",
	"@earendil-works/pi-tui":          "packages/tui/src",
}

var (
	distTypeDecl  = regexp.MustCompile(`^dist/(.+)\.d\.ts$`)
	portMapRowPat = regexp.MustCompile("^\\| `([^`]+)` \\| `([^`]+)` \\| (.+?) \\|")
	parenNote     = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "portreconcile:", err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", ".", "repository root for resolving Go targets and PORT_MAP")
	inventoryPath := flag.String("inventory", "", "upstream inventory JSON (default parity/interfaces/upstream-v<version>.json under root)")
	portMapPath := flag.String("port-map", "PORT_MAP.md", "PORT_MAP path relative to root")
	coveragePath := flag.String("coverage", "", "optional Go coverage profile; splits implemented into covered/uncovered")
	parityCoveragePath := flag.String("parity-coverage", "", "optional byte-faithful parity coverage profile; in -groups mode, tiers implemented as parity/unit/uncovered")
	groups := flag.Bool("groups", false, "emit per-group review bundles instead of the flat ledger")
	scenariosRoot := flag.String("scenarios", "parity/scenarios", "parity scenarios root (relative to root) for scenario attribution")
	coversAudit := flag.String("covers-audit", "", "scenario .toml path; with -parity-coverage from a single-scenario run, flag covers entries whose PORT_MAP Go target was never executed (loose-link detector)")
	portmapFlip := flag.Bool("portmap-flip", false, "derive PORT_MAP status from coverage: flip ⬜ rows whose mapped Go target exists and is covered by -coverage (unit) or -parity-coverage; report the residual gap. Rewrites PORT_MAP.md only with -apply")
	apply := flag.Bool("apply", false, "with -portmap-flip, rewrite PORT_MAP.md in place")
	format := flag.String("format", "markdown", "markdown or json")
	flag.Parse()

	invPath := *inventoryPath
	if invPath == "" {
		invPath = filepath.Join(*root, "parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json")
	}
	decls, err := loadInventory(invPath)
	if err != nil {
		return err
	}
	rows, err := parsePortMap(filepath.Join(*root, *portMapPath))
	if err != nil {
		return err
	}
	var covered map[string]bool
	if *coveragePath != "" {
		covered, err = coveredFiles(*coveragePath)
		if err != nil {
			return err
		}
	}
	if *coversAudit != "" {
		if *parityCoveragePath == "" {
			return fmt.Errorf("-covers-audit requires -parity-coverage <single-scenario piglet>")
		}
		scenarioCov, err := coveredFiles(*parityCoveragePath)
		if err != nil {
			return err
		}
		// Fail closed: Go integration coverage only flushes on a clean pig exit.
		// An interactive-tmux scenario whose pig is SIGKILLed at teardown
		// (driver_tmux.go) writes no counters, yielding an empty piglet. Auditing
		// covers honesty from an empty piglet would falsely flag every entry as a
		// loose link, so refuse rather than emit false positives.
		if len(scenarioCov) == 0 {
			return fmt.Errorf("parity-coverage profile %s captured no executed blocks; "+
				"the scenario's pig subprocess did not flush coverage (common for "+
				"interactive-tmux scenarios killed before exit): cannot audit covers honesty",
				*parityCoveragePath)
		}
		loose, err := auditCovers(os.Stdout, *root, *coversAudit, rows, scenarioCov)
		if err != nil {
			return err
		}
		if loose > 0 {
			return fmt.Errorf("%d covers entries name a Go target the scenario never executed (loose link)", loose)
		}
		return nil
	}
	if *portmapFlip {
		var parityCov map[string]bool
		if *parityCoveragePath != "" {
			parityCov, err = coveredFiles(*parityCoveragePath)
			if err != nil {
				return err
			}
		}
		return flipPortMap(os.Stdout, filepath.Join(*root, *portMapPath), *root, covered, parityCov, *apply)
	}
	if *groups {
		index, err := scenarioCoverage(filepath.Join(*root, *scenariosRoot))
		if err != nil {
			return err
		}
		var parityCov map[string]bool
		if *parityCoveragePath != "" {
			parityCov, err = coveredFiles(*parityCoveragePath)
			if err != nil {
				return err
			}
		}
		return writeGroups(os.Stdout, *format, reconcileGroups(*root, decls, rows, covered, parityCov, covered, index))
	}
	report := reconcile(*root, decls, rows, covered)
	if err := writeReport(os.Stdout, *format, report); err != nil {
		return err
	}
	if report.counts[classGap] > 0 {
		return fmt.Errorf("%d genuine gap-candidate declarations (mapped Go target absent)", report.counts[classGap])
	}
	return nil
}

func loadInventory(path string) ([]declaration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inv inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("decode inventory %s: %w", path, err)
	}
	return inv.Interfaces, nil
}

func parsePortMap(path string) (map[string]portMapRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows := map[string]portMapRow{}
	for line := range strings.SplitSeq(string(data), "\n") {
		match := portMapRowPat.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		rows[match[1]] = portMapRow{target: match[2], disposition: strings.TrimSpace(match[3])}
	}
	return rows, nil
}

// upstreamSourcePath translates a declaration's published .d.ts provenance into
// the tracked upstream monorepo source path PORT_MAP keys on. Re-exports can
// belong to one public package while their declaration comes from another
// tracked package, so an explicit node_modules provenance takes precedence over
// the exporting package. The second result is false for untracked packages or
// non-declaration files.
func upstreamSourcePath(decl declaration) (string, bool) {
	base, ok := packageSourceRoots[decl.Package]
	if !ok {
		return "", false
	}
	sourcePath := decl.Source.Path
	if strings.HasPrefix(sourcePath, "node_modules/") {
		matched := false
		for packageName, sourceRoot := range packageSourceRoots {
			prefix := "node_modules/" + packageName + "/"
			if !strings.HasPrefix(sourcePath, prefix) {
				continue
			}
			base = sourceRoot
			sourcePath = strings.TrimPrefix(sourcePath, prefix)
			matched = true
			break
		}
		if !matched {
			return "", false
		}
	}
	match := distTypeDecl.FindStringSubmatch(sourcePath)
	if match == nil {
		return "", false
	}
	return base + "/" + match[1] + ".ts", true
}

// goTargets resolves the on-disk Go files a PORT_MAP target names, glob-expanded
// and repo-relative. hasGoTarget is false when the target names no Go file (a
// designed-out note). A target may spread one upstream file across several Go
// files, so any resolved file confirms the mapped code region exists.
func goTargets(root, target string) (files []string, hasGoTarget bool) {
	if strings.HasPrefix(strings.TrimSpace(target), "(") {
		return nil, false
	}
	for _, part := range splitTargets(target) {
		if !strings.HasSuffix(part, ".go") && !strings.Contains(part, "*") {
			continue
		}
		hasGoTarget = true
		matches, err := filepath.Glob(filepath.Join(root, part))
		if err != nil {
			continue
		}
		for _, match := range matches {
			rel, err := filepath.Rel(root, match)
			if err != nil {
				continue
			}
			files = append(files, filepath.ToSlash(rel))
		}
	}
	slices.Sort(files)
	return slices.Compact(files), hasGoTarget
}

// coveredFiles reads a Go coverage profile (textfmt) and returns the set of
// repo-relative production files with at least one executed statement. The
// module prefix is stripped so paths align with PORT_MAP's repo-relative targets.
func coveredFiles(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	covered := map[string]bool{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		file, _, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[len(fields)-1] == "0" {
			continue
		}
		covered[strings.TrimPrefix(file, modulePrefix)] = true
	}
	return covered, nil
}

const modulePrefix = "github.com/MichaelKinsy/PiG/"

func splitTargets(target string) []string {
	fields := strings.FieldsFunc(target, func(r rune) bool { return r == '+' || r == ',' })
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, strings.TrimSpace(parenNote.ReplaceAllString(field, "")))
	}
	return parts
}

func classify(root string, decl declaration, rows map[string]portMapRow, covered map[string]bool) classification {
	source, ok := upstreamSourcePath(decl)
	if !ok {
		return classUnjoined
	}
	row, ok := rows[source]
	if !ok {
		return classUnjoined
	}
	switch row.disposition {
	case "🟡", "⏸":
		return classPartialDeferred
	case "n/a":
		return classDesignedOut
	}
	files, hasGoTarget := goTargets(root, row.target)
	if !hasGoTarget {
		return classDesignedOut
	}
	if row.disposition == "✅" {
		return classPortedAudited
	}
	if len(files) == 0 {
		return classGap
	}
	if covered == nil {
		return classImplementedUnaudited
	}
	if slices.ContainsFunc(files, func(file string) bool { return covered[file] }) {
		return classImplementedCovered
	}
	return classImplementedUncovered
}

type reconciliation struct {
	total  int
	counts map[classification]int
	gaps   []string
}

func reconcile(root string, decls []declaration, rows map[string]portMapRow, covered map[string]bool) reconciliation {
	report := reconciliation{counts: map[classification]int{}}
	for _, decl := range decls {
		if decl.ParentID != "" {
			continue
		}
		report.total++
		class := classify(root, decl, rows, covered)
		report.counts[class]++
		if class == classGap {
			report.gaps = append(report.gaps, decl.ID)
		}
	}
	slices.Sort(report.gaps)
	return report
}

// group aggregates one upstream source directory: the natural review chunk. A
// human approves a group's verdict once instead of proving each declaration.
type group struct {
	key       string
	total     int
	counts    map[classification]int
	tiers     map[string]int
	scenarios []string
}

// coverageTier classifies an implemented-unaudited declaration by the strongest
// evidence exercising its mapped Go file: "parity" (a byte-faithful flow compared
// to upstream pi), "unit" (a Go unit test only: the weak floor), or "none" (no
// test: the review flag). Returns "" for declarations that are not
// implemented-unaudited (audited, designed-out, partial, gap, unjoined), which
// carry their own disposition and need no coverage tier.
func coverageTier(root string, decl declaration, rows map[string]portMapRow, parityCov, unitCov map[string]bool) string {
	source, ok := upstreamSourcePath(decl)
	if !ok {
		return ""
	}
	row, ok := rows[source]
	if !ok {
		return ""
	}
	switch row.disposition {
	case "✅", "🟡", "⏸", "n/a":
		return ""
	}
	files, hasGoTarget := goTargets(root, row.target)
	if !hasGoTarget || len(files) == 0 {
		return ""
	}
	if slices.ContainsFunc(files, func(file string) bool { return parityCov[file] }) {
		return "parity"
	}
	if slices.ContainsFunc(files, func(file string) bool { return unitCov[file] }) {
		return "unit"
	}
	return "none"
}

// upstreamGroupKey is the upstream source directory a declaration belongs to,
// e.g. packages/ai/src/providers. Members are excluded by the caller, so a group
// counts only top-level declarations. A declaration whose source path cannot be
// derived (unknown package) falls in one safety bucket; this is distinct from the
// unjoined classification, which means the source is known but has no PORT_MAP row.
func upstreamGroupKey(decl declaration) string {
	source, ok := upstreamSourcePath(decl)
	if !ok {
		return "(unplaceable)"
	}
	return path.Dir(source)
}

// auditCovers reports, for one scenario, which of its covers entries had the
// mapped PORT_MAP Go target actually executed during a single-scenario parity
// run (the parity-coverage profile). A covers entry whose Go target file has
// zero coverage is a proven loose link: the scenario claims the file but never
// ran it. The check is conservative: file-level coverage cannot prove the
// *right* function ran, only that the file was untouched, so it has no false
// positives and known false negatives (a target touched at startup passes).
func auditCovers(w io.Writer, root, scenarioPath string, rows map[string]portMapRow, covered map[string]bool) (loose int, err error) {
	var doc struct {
		Name   string   `toml:"name"`
		Covers []string `toml:"covers"`
	}
	if _, err := toml.DecodeFile(scenarioPath, &doc); err != nil {
		return 0, fmt.Errorf("decode scenario %s: %w", scenarioPath, err)
	}
	name := doc.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(scenarioPath), ".toml")
	}
	_, _ = fmt.Fprintf(w, "# covers audit: %s\n\n", name)
	_, _ = fmt.Fprintf(w, "| covers (upstream) | pig target | status |\n|---|---|---|\n")
	for _, up := range doc.Covers {
		row, ok := rows[up]
		if !ok {
			_, _ = fmt.Fprintf(w, "| `%s` | (no PORT_MAP row) | unmapped |\n", up)
			continue
		}
		files, hasGo := goTargets(root, row.target)
		if !hasGo {
			_, _ = fmt.Fprintf(w, "| `%s` | `%s` | no-go-target |\n", up, row.target)
			continue
		}
		if slices.ContainsFunc(files, func(f string) bool { return covered[f] }) {
			_, _ = fmt.Fprintf(w, "| `%s` | `%s` | exercised |\n", up, row.target)
			continue
		}
		_, _ = fmt.Fprintf(w, "| `%s` | `%s` | LOOSE (target not executed) |\n", up, row.target)
		loose++
	}
	return loose, nil
}

// portMapLinePat isolates the three cells of a PORT_MAP table row so the status
// token (group 2) can be rewritten while preserving the rest of the line.
var portMapLinePat = regexp.MustCompile("^(\\| `[^`]+` \\| `[^`]+` \\| )(.+?)( \\|.*)$")

// flipPortMap derives PORT_MAP status from coverage. A ⬜ row flips to ✅ when
// its mapped Go target exists and is exercised by unit or parity tests; PORT_MAP
// ✅ means ported+mapped (verification quality stays in coverage.md), and the
// coverage overlay additionally proves the mapping is real and under test. Rows
// whose target is a designed-out note, names a Go file that does not exist, or
// exists but is uncovered are left ⬜ and reported as the residual gap. 🟡/⏸/🔴
// are deliberate states and are never touched.
func flipPortMap(w io.Writer, portMapPath, root string, unitCov, parityCov map[string]bool, apply bool) error {
	data, err := os.ReadFile(portMapPath)
	if err != nil {
		return err
	}
	covered := func(files []string) bool {
		for _, f := range files {
			if unitCov[f] || parityCov[f] {
				return true
			}
		}
		return false
	}
	lines := strings.Split(string(data), "\n")
	var flipped, note, missing, unverified int
	var missingList, unverifiedList []string
	for i, line := range lines {
		m := portMapRowPat.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || strings.TrimSpace(m[3]) != "⬜" {
			continue
		}
		files, hasGo := goTargets(root, m[2])
		switch {
		case !hasGo:
			note++
		case len(files) == 0:
			missing++
			missingList = append(missingList, m[1]+" → "+m[2])
		case covered(files):
			lines[i] = portMapLinePat.ReplaceAllString(line, "${1}✅${3}")
			flipped++
		default:
			unverified++
			unverifiedList = append(unverifiedList, m[1]+" → "+m[2])
		}
	}
	_, _ = fmt.Fprintf(w, "# PORT_MAP flip (upstream %s)\n\n", coding.UpstreamVersion)
	_, _ = fmt.Fprintf(w, "- flipped ⬜ → ✅ (mapped Go target exists and is test-covered): %d\n", flipped)
	_, _ = fmt.Fprintf(w, "- left ⬜: designed-out note, no Go target: %d\n", note)
	_, _ = fmt.Fprintf(w, "- left ⬜: mapped Go file MISSING (genuine gap to pi): %d\n", missing)
	_, _ = fmt.Fprintf(w, "- left ⬜: Go file exists but UNCOVERED by our tests: %d\n\n", unverified)
	for _, p := range missingList {
		_, _ = fmt.Fprintf(w, "  gap: %s\n", p)
	}
	for _, p := range unverifiedList {
		_, _ = fmt.Fprintf(w, "  unverified: %s\n", p)
	}
	if !apply {
		_, _ = fmt.Fprintln(w, "\ndry-run: pass -apply to rewrite PORT_MAP.md")
		return nil
	}
	if err := os.WriteFile(portMapPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "\napplied: PORT_MAP.md rewritten (%d rows flipped ⬜ → ✅)\n", flipped)
	return nil
}

// whose covers list names a file in it. This is the byte-faithful-flow evidence a
// group carries into review: which comparisons already exercise the group.
func scenarioCoverage(scenariosRoot string) (map[string][]string, error) {
	index := map[string]map[string]bool{}
	err := filepath.WalkDir(scenariosRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".toml") {
			return err
		}
		var doc struct {
			Covers []string `toml:"covers"`
		}
		if _, err := toml.DecodeFile(p, &doc); err != nil {
			return fmt.Errorf("decode scenario %s: %w", p, err)
		}
		rel, err := filepath.Rel(scenariosRoot, p)
		if err != nil {
			rel = p
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), ".toml")
		for _, covered := range doc.Covers {
			key := path.Dir(covered)
			if index[key] == nil {
				index[key] = map[string]bool{}
			}
			index[key][name] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for key, names := range index {
		out[key] = slices.Sorted(maps.Keys(names))
	}
	return out, nil
}

func reconcileGroups(root string, decls []declaration, rows map[string]portMapRow, covered, parityCov, unitCov map[string]bool, scenarios map[string][]string) []group {
	tiered := parityCov != nil || unitCov != nil
	byKey := map[string]*group{}
	for _, decl := range decls {
		if decl.ParentID != "" {
			continue
		}
		key := upstreamGroupKey(decl)
		g, ok := byKey[key]
		if !ok {
			g = &group{key: key, counts: map[classification]int{}, tiers: map[string]int{}, scenarios: scenarios[key]}
			byKey[key] = g
		}
		g.total++
		g.counts[classify(root, decl, rows, covered)]++
		if tiered {
			if tier := coverageTier(root, decl, rows, parityCov, unitCov); tier != "" {
				g.tiers[tier]++
			}
		}
	}
	groups := make([]group, 0, len(byKey))
	for _, g := range byKey {
		groups = append(groups, *g)
	}
	slices.SortFunc(groups, func(a, b group) int { return strings.Compare(a.key, b.key) })
	return groups
}

func writeGroups(w *os.File, format string, groups []group) error {
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		payload := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			counts := map[string]int{}
			for class, count := range g.counts {
				counts[string(class)] = count
			}
			payload = append(payload, map[string]any{
				"group": g.key, "total": g.total, "counts": counts, "tiers": g.tiers, "scenarios": g.scenarios,
			})
		}
		return encoder.Encode(map[string]any{"upstreamVersion": coding.UpstreamVersion, "groups": payload})
	}
	_, _ = fmt.Fprintf(w, "# Grouped port reconciliation (upstream %s): %d review groups\n\n", coding.UpstreamVersion, len(groups))
	for _, g := range groups {
		_, _ = fmt.Fprintf(w, "## %s (%d top-level)\n\n", g.key, g.total)
		for _, class := range classOrder {
			if g.counts[class] > 0 {
				_, _ = fmt.Fprintf(w, "- %s: %d\n", class, g.counts[class])
			}
		}
		if g.tiers["parity"]+g.tiers["unit"]+g.tiers["none"] > 0 {
			_, _ = fmt.Fprintf(w, "- implemented coverage tier: parity %d \u00b7 unit-only %d \u00b7 uncovered %d\n",
				g.tiers["parity"], g.tiers["unit"], g.tiers["none"])
		}
		if len(g.scenarios) > 0 {
			_, _ = fmt.Fprintf(w, "- parity scenarios (%d): %s\n", len(g.scenarios), strings.Join(g.scenarios, ", "))
		} else {
			_, _ = fmt.Fprintln(w, "- parity scenarios: none claim this group")
		}
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

func writeReport(w *os.File, format string, report reconciliation) error {
	if format == "json" {
		payload := map[string]any{
			"upstreamVersion": coding.UpstreamVersion,
			"totalTopLevel":   report.total,
			"counts":          map[string]int{},
			"gapCandidates":   report.gaps,
		}
		for class, count := range report.counts {
			payload["counts"].(map[string]int)[string(class)] = count
		}
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	}
	_, _ = fmt.Fprintf(w, "# Port reconciliation (upstream %s)\n\n", coding.UpstreamVersion)
	_, _ = fmt.Fprintf(w, "Top-level upstream declarations: %d\n\n", report.total)
	_, _ = fmt.Fprintln(w, "| classification | count |")
	_, _ = fmt.Fprintln(w, "|---|---:|")
	for _, class := range classOrder {
		_, _ = fmt.Fprintf(w, "| %s | %d |\n", class, report.counts[class])
	}
	if len(report.gaps) > 0 {
		_, _ = fmt.Fprintf(w, "\n## Gap candidates (%d)\n\n", len(report.gaps))
		for _, id := range report.gaps {
			_, _ = fmt.Fprintf(w, "- %s\n", id)
		}
	}
	return nil
}
