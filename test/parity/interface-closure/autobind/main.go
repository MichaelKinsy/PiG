// Command autobind is a deterministic gap detector for the Pi interface ledger. For every interface ID it applies a fixed rule set
// and decides NOT-A-GAP or GAP(reason): a Go symbol exists under the name rules, its shape is compatible with the upstream
// declaration, and it is exercised by a Go test that asserts on it or by code reachable from cmd/pig. A rule that cannot decide
// is a gap ("undecidable"), never a pass. The same input gives the same output: there are no scores, no network and no model.
// The tool never edits the mapping.
//
// Usage: go run ./test/parity/interface-closure/autobind [flags]
//
//	-out dir        write gaps.tsv, not-a-gap.tsv and summary.md (default build/interface-gaps)
//	-check          compare the gap list with the committed baseline and fail when it grows (make interface-gaps)
//	-update         rewrite the baseline
//	-closed-check   trust check 1: run the rules on the rows closed by hand and list every disagreement
//	-blind          ignore documented renames (with -closed-check: measure how many closed rows need one)
//	-seed-renames   write renames.json from the hand-closed rows whose Go name the name rules cannot find
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

const (
	renamesFile         = "test/parity/interface-closure/autobind/renames.json"
	reviewedRenamesFile = "test/parity/interface-closure/autobind/renames-reviewed.json"
	placementsFile      = "test/parity/interface-closure/autobind/placements-reviewed.json"
	representationsFile = "test/parity/interface-closure/autobind/representations.json"
	structuralFile      = "test/parity/interface-closure/autobind/structural-reviewed.json"
	holdsFile           = "test/parity/interface-closure/autobind/holds.json"
	reviewedTypesFile   = "test/parity/interface-closure/autobind/reviewed-types.json"
	reviewedGapsFile    = "test/parity/interface-closure/autobind/reviewed-gaps.json"
	baselineFile        = "test/parity/interface-closure/gaps/interface-gaps-v%s.tsv"
)

type options struct {
	root, version, reach, out                  string
	cache, frontier                            string
	importDecisions                            bool
	check, update, closed, blind, seed, mutate bool
}

func main() {
	var o options
	flag.StringVar(&o.root, "root", ".", "repository root")
	flag.StringVar(&o.version, "version", pigversion.UpstreamVersion, "upstream version of the ledger")
	flag.StringVar(&o.reach, "reach", "", "reachable-function list from cmd/prodreach; computed when empty")
	flag.StringVar(&o.out, "out", "build/interface-gaps", "output directory")
	flag.StringVar(&o.cache, "cache", "", `derivation cache directory, or "auto" for $PIG_LEDGER_CACHE (else build/ledger-cache); empty derives from scratch`)
	flag.StringVar(&o.frontier, "frontier", "", "write the ranked list of root gaps to this TSV file")
	flag.BoolVar(&o.check, "check", false, "fail when the gap list grows past the committed baseline")
	flag.BoolVar(&o.update, "update", false, "rewrite the committed baseline")
	flag.BoolVar(&o.importDecisions, "import-decisions", false, "with -update, add the mapping's designed-out and divergence rows that reviewed-decisions.json lacks")
	flag.BoolVar(&o.closed, "closed-check", false, "run the rules on the rows closed by hand and list the disagreements")
	flag.BoolVar(&o.blind, "blind", false, "ignore documented renames")
	flag.BoolVar(&o.mutate, "mutate", false, "run the Pi-matched tests of library methods against a mutant and record the kills in mutation-checked.json")
	flag.BoolVar(&o.seed, "seed-renames", false, "write renames.json from the hand-closed rows")
	flag.StringVar(&handLedger, "hand-ledger", "", "mapping file to read instead of the committed one (trust check on the ledger as it was closed by hand)")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "autobind:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	root, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	// Parts of the engine find the repository from the working directory (the PORT_MAP placement rule), so the working directory is the root.
	if err := os.Chdir(root); err != nil {
		return err
	}
	l, err := loadLedger(root, o.version)
	if err != nil {
		return err
	}
	cache, err := openCache(o.cache, root, os.Stderr)
	if err != nil {
		return err
	}
	// The cache holds the derivation of the standard path only: the trust checks, the rename seeding and the mutation run need the
	// detector itself.
	cacheable := cache != nil && !o.blind && !o.seed && !o.closed && !o.mutate && handLedger == ""
	reachOf := func() (map[string]bool, error) { return loadReach(root, o.reach) }
	var det *detector
	var dv *derivation
	if cacheable {
		reachOf = func() (map[string]bool, error) { return cachedReach(cache, root, o) }
		dv, err = cachedDerive(cache, root, o, l, reachOf)
	} else {
		det, dv, err = derive(root, o, l, reachOf)
	}
	if err != nil {
		return err
	}
	// A stale reviewed exception fails the run, after the reports are written: they describe the derivation either way.
	var reviewedFail error
	if !o.blind {
		var reviewed map[string]reviewedType
		if err := readJSON(filepath.Join(root, reviewedTypesFile), &reviewed); err != nil && !os.IsNotExist(err) {
			return err
		}
		if stale := staleReviewedTypes(reviewed, dv.usedSet()); len(stale) > 0 && o.update {
			// An update prunes the reviewed types no row uses any more, as it prunes the reviewed gaps.
			fmt.Printf("pruned %d stale reviewed types: %s\n", len(stale), strings.Join(stale, ", "))
			if err := pruneReviewedTypes(filepath.Join(root, reviewedTypesFile), reviewed, stale); err != nil {
				return err
			}
		}
		reviewedFail = reviewedErr(reviewed, dv.usedSet())
	}
	decisions := dv.Decisions
	stale, gaps, err := applyReviewInputs(root, decisions, o.blind, o.seed)
	if err != nil {
		return err
	}
	if reviewedFail != nil {
		if !o.mutate && !o.seed && !o.closed {
			_ = writeReport(o.out, decisions)
			if o.frontier != "" {
				_ = writeFrontier(o.frontier, root, l, decisions)
			}
		}
		return reviewedFail
	}
	switch {
	case len(stale) > 0 && o.update:
		// An update prunes the reviews whose rows are decided now, so a lane that closes a row by porting does not block the next regeneration.
		fmt.Printf("pruned %d stale reviewed gaps: %s\n", len(stale), strings.Join(stale, ", "))
		if err := pruneReviewedGaps(filepath.Join(root, reviewedGapsFile), gaps, stale); err != nil {
			return err
		}
	case len(stale) > 0:
		return fmt.Errorf("%s: %d stale entries (the row is not undecided): %s", reviewedGapsFile, len(stale), strings.Join(stale, ", "))
	}
	switch {
	case o.mutate:
		if det.lib == nil {
			det.lib = newLibrary(root)
		}
		return det.lib.runMutants(det)
	case o.seed:
		return seedRenames(root, l, decisions)
	case o.closed:
		return closedCheck(o.out, l, decisions, o.blind)
	}
	if err := writeReport(o.out, decisions); err != nil {
		return err
	}
	if o.frontier != "" {
		if err := writeFrontier(o.frontier, root, l, decisions); err != nil {
			return err
		}
	}
	baseline := filepath.Join(root, fmt.Sprintf(baselineFile, o.version))
	mappingPath := filepath.Join(root, "test/parity/interfaces", "mapping-v"+o.version+".json")
	decisionsPath := filepath.Join(root, reviewedDecisionsFile)
	derived, st, decided, err := derivedMapping(l, mappingPath, decisionsPath, decisions, o.importDecisions && o.update)
	if err != nil {
		return err
	}
	fmt.Printf("ledger: %d derived rows written, %d reopened, %d kept (hand rows, divergences, designed-out), %d not derivable; reviewed decisions: %d applied (%d restored), %d stale, %d imported\n", st.Derived, st.Reopened, st.Kept, st.Underivable, st.Decisions.Applied, st.Decisions.Restored, st.Decisions.Stale, st.Decisions.Imported)
	if o.update {
		if err := os.MkdirAll(filepath.Dir(baseline), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(baseline, []byte(gapTSV(decisions, false)), 0o644); err != nil {
			return err
		}
		// The file keeps one compact line per ID so lanes merge it without conflicts; an update writes it in that form whenever it differs
		// (an import added rows, or a lane merged it with another encoder).
		data, err := encodeReviewedDecisions(decided)
		if err != nil {
			return err
		}
		if err := writeDecisionsIfChanged(decisionsPath, data, st.Decisions.Imported > 0); err != nil {
			return err
		}
		return os.WriteFile(mappingPath, derived, 0o644)
	}
	if o.check {
		current, err := os.ReadFile(mappingPath)
		if err != nil {
			return err
		}
		if err := checkBaseline(baseline, decisions); err != nil {
			return err
		}
		if !bytes.Equal(current, derived) {
			return fmt.Errorf("the ledger %s is not what the detector derives: run make interface-gaps-update and commit the result", mappingPath)
		}
	}
	return nil
}

// derive runs the rules over the ledger: it loads the reach graph, builds the Go index, applies the documented renames and reviewed inputs,
// and decides every ID. It returns the detector for the callers that read its internals; the caller checks the reviewed type exceptions
// (detector.checkReviewed).
//
// The reach graph comes first and the index second, not concurrently: prodreach peaks near 8 GB and the index near 5 GB, and a lane runs
// under a 12 GB cap, where both together push the collector into a limit-driven loop that costs minutes.
func derive(root string, o options, l *ledger, reachOf func() (map[string]bool, error)) (*detector, *derivation, error) {
	reach, err := reachOf()
	if err != nil {
		return nil, nil, err
	}
	ix, err := buildIndex(root, reach, []string{"./..."})
	if err != nil {
		return nil, nil, err
	}
	renames := renameTable{}
	if !o.blind && !o.seed {
		if err := readJSON(filepath.Join(root, renamesFile), &renames); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
	reviewed := map[string]string{}
	if !o.blind && !o.seed {
		if err := readJSON(filepath.Join(root, reviewedRenamesFile), &reviewed); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
	for _, e := range l.entries {
		if parts := parseID(e.ID); e.Role == "" && parts.Member == "" {
			if target, ok := reviewed[parts.Pkg+":"+e.Name]; ok && (e.Kind == "interface" || e.Kind == "class" || e.Kind == "type-alias") {
				renames[e.ID] = target
			}
		}
	}
	det := newDetector(ix, l, renames, tsAliases(root))
	if !o.blind {
		if err := readJSON(filepath.Join(root, representationsFile), &det.reps); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		var reviewedForms map[string]struct {
			Go     []string `json:"go"`
			Reason string   `json:"reason"`
		}
		if err := readJSON(filepath.Join(root, structuralFile), &reviewedForms); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		det.forms = map[string][]string{}
		for k, v := range reviewedForms {
			det.forms[k] = v.Go
		}
		if err := readJSON(filepath.Join(root, reviewedTypesFile), &det.reviewed); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		if err := readJSON(filepath.Join(root, narrowFile), &det.narrow); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		if err := readJSON(filepath.Join(root, placementsFile), &det.placements); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
	decisions := det.all()
	if err := checkDistinctDecisions(decisions); err != nil {
		return nil, nil, err
	}
	if err := det.checkPlacements(); err != nil {
		return nil, nil, err
	}
	return det, &derivation{Decisions: decisions, ReviewedUsed: slices.Sorted(maps.Keys(det.reviewedUsed))}, nil
}

// cachedDerive returns the derivation for the current tree from the shared cache, computing and publishing it on a miss. The caller checks
// the reviewed type exceptions against the derivation's usage, as it does for a derivation from scratch.
func cachedDerive(c *ledgerCache, root string, o options, l *ledger, reachOf func() (map[string]bool, error)) (*derivation, error) {
	start := time.Now()
	key, err := derivationKey(root, o)
	if err != nil {
		return nil, err
	}
	if dv, ok := c.loadDerivation(key); ok {
		c.logf("ledger-cache: derivation %s hit (%s)", key[:8], time.Since(start).Round(time.Millisecond))
		return dv, nil
	}
	release := c.lock("derive", key)
	defer release()
	if dv, ok := c.loadDerivation(key); ok {
		c.logf("ledger-cache: derivation %s hit after waiting (%s)", key[:8], time.Since(start).Round(time.Millisecond))
		return dv, nil
	}
	c.logf("ledger-cache: derivation %s miss; deriving", key[:8])
	_, dv, err := derive(root, o, l, reachOf)
	if err != nil {
		return nil, err
	}
	// A file edited while the rules ran makes the derivation a mix of two trees; it is returned but never stored under either key.
	if after, err := derivationKey(root, o); err != nil || after != key {
		c.logf("ledger-cache: derivation %s not stored: the inputs changed while deriving", key[:8])
		return dv, nil
	}
	meta := map[string]any{"input": key, "head": gitHead(root), "created": time.Now().UTC().Format(time.RFC3339), "seconds": time.Since(start).Seconds()}
	if err := c.storeDerivation(key, dv, meta); err != nil {
		c.logf("ledger-cache: not stored: %v", err)
	}
	c.prune()
	c.logf("ledger-cache: derivation %s derived in %s", key[:8], time.Since(start).Round(time.Millisecond))
	return dv, nil
}

func gitHead(root string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// prodreachArgs is the invocation the ledger uses.
var (
	reachTags     = []string{"pig_experimental"}
	reachPatterns = []string{"./cmd/pig", "./cmd/pig-experimental"}
)

// cachedReach returns the reach graph of the current tree from the shared cache, running prodreach on a miss.
func cachedReach(c *ledgerCache, root string, o options) (map[string]bool, error) {
	if o.reach != "" {
		return loadReach(root, o.reach)
	}
	start := time.Now()
	key, err := reachKey(root, reachTags, reachPatterns)
	if err != nil {
		return nil, err
	}
	finish := func(list []string) map[string]bool {
		reach := make(map[string]bool, len(list))
		for _, k := range list {
			reach[k] = true
		}
		return reach
	}
	if list, ok := c.loadReach(key); ok {
		c.logf("ledger-cache: reach %s hit (%s)", key[:8], time.Since(start).Round(time.Millisecond))
		return finish(list), nil
	}
	release := c.lock("reach", key)
	defer release()
	if list, ok := c.loadReach(key); ok {
		c.logf("ledger-cache: reach %s hit after waiting (%s)", key[:8], time.Since(start).Round(time.Millisecond))
		return finish(list), nil
	}
	c.logf("ledger-cache: reach %s miss; running prodreach", key[:8])
	data, err := runProdreach(root)
	if err != nil {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	if err := c.storeReach(key, data); err != nil {
		c.logf("ledger-cache: reach not stored: %v", err)
	}
	c.logf("ledger-cache: reach %s computed in %s", key[:8], time.Since(start).Round(time.Millisecond))
	return finish(list), nil
}

// runProdreach computes the reach graph and returns the JSON list.
func runProdreach(root string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "autobind-reach")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	path := filepath.Join(tmp, "reach.json")
	args := []string{"run", "./test/parity/cmd/prodreach", "-out", path}
	for _, t := range reachTags {
		args = append(args, "-tags", t)
	}
	cmd := exec.Command("go", append(args, reachPatterns...)...)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("prodreach: %w", err)
	}
	return os.ReadFile(path)
}

// applyReviewInputs applies the reviewed gaps and the holds to the decisions and returns the reviewed gaps that no undecided row uses, with
// the reviewed-gaps file's content. A blind run applies no reviewed gaps; a seeding run applies no holds.
func applyReviewInputs(root string, decisions []*decision, blind, seed bool) (stale []string, gaps map[string]string, err error) {
	gaps = map[string]string{}
	if !blind {
		if err := readJSON(filepath.Join(root, reviewedGapsFile), &gaps); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		if stale, err = applyReviewedGaps(decisions, gaps); err != nil {
			return nil, nil, err
		}
	}
	if !seed {
		holds := map[string]string{}
		if err := readJSON(filepath.Join(root, holdsFile), &holds); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		if err := applyHolds(decisions, holds); err != nil {
			return nil, nil, err
		}
	}
	return stale, gaps, nil
}

// writeDecisionsIfChanged writes the encoded reviewed decisions when force is set or the file holds other bytes (it was merged with another
// encoder), and leaves an identical file untouched.
func writeDecisionsIfChanged(path string, data []byte, force bool) error {
	if current, err := os.ReadFile(path); !force && err == nil && bytes.Equal(current, data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}

// derivedMapping returns the mapping file as the detector derives it: the reviewed decisions are applied first, then every other row is
// derived. It also returns the reviewed decisions, which gain the imported rows when importMissing is set.
func derivedMapping(l *ledger, path, decisionsPath string, ds []*decision, importMissing bool) ([]byte, syncStats, reviewedDecisions, error) {
	doc, err := readMapping(path)
	if err != nil {
		return nil, syncStats{}, nil, err
	}
	reviewed, err := readReviewedDecisions(decisionsPath)
	if err != nil {
		return nil, syncStats{}, nil, err
	}
	byID := make(map[string]*decision, len(ds))
	for _, d := range ds {
		byID[d.ID] = d
	}
	applied, err := applyReviewedDecisions(doc, reviewed, func(id string) (bool, bool) {
		d := byID[id]
		return d != nil && !d.Gap, d != nil && d.DesignedOut != ""
	}, importMissing)
	if err != nil {
		return nil, syncStats{}, nil, err
	}
	st := syncMapping(l, doc, byID)
	st.Decisions = applied
	out, err := encodeMapping(doc)
	return out, st, reviewed, err
}

func loadReach(root, path string) (map[string]bool, error) {
	var data []byte
	if path == "" {
		var err error
		if data, err = runProdreach(root); err != nil {
			return nil, err
		}
	} else {
		var err error
		if data, err = os.ReadFile(path); err != nil {
			return nil, err
		}
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("reach list: %w", err)
	}
	reach := make(map[string]bool, len(list))
	for _, k := range list {
		reach[k] = true
	}
	return reach, nil
}

// gapTSV lists the gaps sorted by ID: ID, package, reason, candidate symbol (and the detail for the full report).
func gapTSV(ds []*decision, detail bool) string {
	var b strings.Builder
	for _, d := range ds {
		if !d.Gap {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s", d.ID, d.Pkg, d.Reason, d.Candidate)
		if detail {
			fmt.Fprintf(&b, "\t%s", strings.Join(strings.Fields(d.Detail), " "))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func writeReport(out string, ds []*decision) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	var ok strings.Builder
	for _, d := range ds {
		if !d.Gap {
			fmt.Fprintf(&ok, "%s\t%s\t%s\t%s\n", d.ID, d.Pkg, d.Candidate, d.Evidence)
		}
	}
	files := map[string]string{"gaps.tsv": gapTSV(ds, true), "not-a-gap.tsv": ok.String(), "summary.md": summary(ds)}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(out, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	fmt.Print(summary(ds))
	return nil
}

func pkgKey(d *decision) string {
	if d.Pkg == "" {
		return "cli"
	}
	return d.Pkg
}

// summary groups the result by reason and package, and by ledger disposition.
func summary(ds []*decision) string {
	reasons := map[string]map[string]int{}
	pkgSet := map[string]bool{}
	notGap := 0
	disp := map[string][2]int{}
	for _, d := range ds {
		c := disp[d.Disp]
		if d.Gap {
			c[1]++
			if reasons[d.Reason] == nil {
				reasons[d.Reason] = map[string]int{}
			}
			reasons[d.Reason][pkgKey(d)]++
			pkgSet[pkgKey(d)] = true
		} else {
			c[0]++
			notGap++
		}
		disp[d.Disp] = c
	}
	pkgs := sortedKeys(pkgSet)
	var b strings.Builder
	fmt.Fprintf(&b, "total IDs: %d\nNOT-A-GAP: %d\nGAP: %d\n\n| reason |", len(ds), notGap, len(ds)-notGap)
	for _, p := range pkgs {
		fmt.Fprintf(&b, " %s |", p)
	}
	b.WriteString(" total |\n|---|")
	for range pkgs {
		b.WriteString("---:|")
	}
	b.WriteString("---:|\n")
	var rs []string
	for r := range reasons {
		rs = append(rs, r)
	}
	sort.Strings(rs)
	for _, r := range rs {
		total := 0
		fmt.Fprintf(&b, "| %s |", r)
		for _, p := range pkgs {
			fmt.Fprintf(&b, " %d |", reasons[r][p])
			total += reasons[r][p]
		}
		fmt.Fprintf(&b, " %d |\n", total)
	}
	b.WriteString("\n| ledger disposition | not a gap | gap |\n|---|---:|---:|\n")
	var dk []string
	for k := range disp {
		dk = append(dk, k)
	}
	sort.Strings(dk)
	for _, k := range dk {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", k, disp[k][0], disp[k][1])
	}
	return b.String()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkBaseline fails when a gap exists that the committed baseline does not list.
func checkBaseline(path string, ds []*decision) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("baseline %s: %w (run with -update to create it)", path, err)
	}
	known := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		id, _, _ := strings.Cut(line, "\t")
		known[id] = true
	}
	var grown []string
	now := 0
	for _, d := range ds {
		if d.Gap {
			now++
			if !known[d.ID] {
				grown = append(grown, fmt.Sprintf("%s\t%s\t%s", d.ID, d.Reason, d.Detail))
			}
		}
	}
	if len(grown) > 0 {
		return fmt.Errorf("the gap list grew: %d gap(s) are not in %s:\n%s", len(grown), path, strings.Join(firstN(grown, 50), "\n"))
	}
	fmt.Printf("interface-gaps: %d gaps, baseline %d, no new gap\n", now, len(known))
	return nil
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// seedRenames writes the documented-rename input: for each hand-closed row whose recorded Go target does not carry the upstream
// name under rules N1 and N2 (or N3 for a constructor), that target. A rename is a recorded decision, not a rule result.
func seedRenames(root string, l *ledger, _ []*decision) error {
	out := renameTable{}
	for _, e := range l.entries {
		row := l.mapping[e.ID]
		if row == nil || row.Disposition != "ported" || len(row.Targets) == 0 || !isUnit(e.ID) {
			continue
		}
		parts := parseID(e.ID)
		if parts.IsCLI {
			continue
		}
		_, sym, _ := strings.Cut(row.Targets[0], "#")
		goName := sym[strings.LastIndex(sym, ".")+1:]
		switch {
		case parts.Construct != "":
			if ctorRule(parts.Name, goName) != "" {
				continue
			}
		case parts.Member != "":
			if nameRule(e.Shape.Name, goName) != "" && ownerFollowsParent(l, e, sym) {
				continue
			}
		default:
			if nameRule(e.Name, goName) != "" && !strings.Contains(sym, ".") {
				continue
			}
		}
		out[e.ID] = row.Targets[0]
	}
	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return err
	}
	fmt.Printf("seeded %d renames\n", len(out))
	return os.WriteFile(filepath.Join(root, renamesFile), append(data, '\n'), 0o644)
}

// ownerFollowsParent reports whether the Go owner of a member target is the type the parent row maps to under the name rules.
func ownerFollowsParent(l *ledger, e *upstreamEntry, sym string) bool {
	owner, _, ok := strings.Cut(sym, ".")
	parent := l.byID[e.ParentID]
	if !ok || parent == nil {
		return false
	}
	return nameRule(parent.Name, owner) != ""
}

func sortByID(ds []*decision) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
}
