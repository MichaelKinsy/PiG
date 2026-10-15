package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// oracleNameRE names tests that say they compare PiG with Pi or Node, run random inputs, or check ordering, cancellation or
// concurrency. Block coverage cannot show what such a test protects, so it is never a candidate.
var oracleNameRE = regexp.MustCompile(`(?i)upstream|oracle|matches(?:pi|node|pinned)|pinnedpi|golden|conformance|parity|random|fuzz|property|Pi[A-Z]|[a-z]Pi(?:[A-Z]|$)|race|concurren|serializ|reentran|ordered|queue|parallel|cancel|abort|lock|shutdown|leak`)

// testBlocks is the set of blocks one test covers when it runs alone.
type testBlocks struct {
	name   string
	blocks map[string]bool
	err    error
}

func listTests(c *coverRun) ([]string, error) {
	dir := filepath.Join(c.root, strings.TrimPrefix(c.pkg, "./"))
	cmd := exec.Command(c.bin, "-test.list", "^(Test|Fuzz)")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "Test") || strings.HasPrefix(line, "Fuzz") {
			names = append(names, line)
		}
	}
	return names, nil
}

func perTestBlocks(c *coverRun, names []string, workers int, work string) []testBlocks {
	results := make([]testBlocks, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out := filepath.Join(work, fmt.Sprintf("t%d.out", i))
			results[i].name = name
			if err := c.run(out, "-test.run=^"+name+"$"); err != nil {
				results[i].err = err
				return
			}
			p, err := readProfile(out)
			if err != nil {
				results[i].err = err
				return
			}
			_ = os.Remove(out)
			set := map[string]bool{}
			for k, covered := range p {
				if covered {
					set[k] = true
				}
			}
			results[i].blocks = set
		}()
	}
	wg.Wait()
	return results
}

// subsumed picks the candidates whose every block another kept test covers.
// Candidates leave in order of fewest blocks first, so a test that adds the
// least goes first, and a test never leaves when it is the last cover of a block.
func subsumed(all []testBlocks, extra map[string]bool, candidate func(name string) bool) []string {
	count := map[string]int{}
	for b, covered := range extra {
		if covered {
			count[b]++
		}
	}
	for _, t := range all {
		for b := range t.blocks {
			count[b]++
		}
	}
	var order []testBlocks
	for _, t := range all {
		if t.err == nil && len(t.blocks) > 0 && candidate(t.name) {
			order = append(order, t)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return len(order[i].blocks) < len(order[j].blocks) })
	var out []string
	for _, t := range order {
		covered := true
		for b := range t.blocks {
			if count[b] < 2 {
				covered = false
				break
			}
		}
		if !covered {
			continue
		}
		for b := range t.blocks {
			count[b]--
		}
		out = append(out, t.name)
	}
	sort.Strings(out)
	return out
}

func runRedundant(root string, args []string) error {
	fs := flag.NewFlagSet("redundant", flag.ExitOnError)
	pkg := fs.String("pkg", "", "package, e.g. ./internal/chord")
	tags := fs.String("tags", "", "build tags")
	workers := fs.Int("j", 8, "parallel single-test runs")
	timeout := fs.Duration("timeout", 10*time.Minute, "per test timeout")
	protect := fs.String("classes", "A-oracle,A-cited,B-invariant", "inventory classes that are never candidates")
	out := fs.String("out", "", "write the candidate test names here")
	journeys := fs.String("journeys", "", "cover profile (text format) of the end-to-end journeys; a block they reach counts as covered by another test")
	only := fs.String("only-classes", "", "when set, only tests of these inventory classes are candidates (for example D-shape)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pkg == "" {
		return fmt.Errorf("-pkg is required")
	}
	classOf, err := classesInPackage(root, strings.TrimPrefix(*pkg, "./"))
	if err != nil {
		return err
	}
	protected := classSet(*protect)
	work, err := os.MkdirTemp("", "testprune-redundant-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	cr, err := newCoverRun(root, *pkg, "", *tags, work, *timeout)
	if err != nil {
		return err
	}
	names, err := listTests(cr)
	if err != nil {
		return err
	}
	all := perTestBlocks(cr, names, *workers, work)
	failed, empty := 0, 0
	for _, t := range all {
		if t.err != nil {
			failed++
		} else if len(t.blocks) == 0 {
			empty++
		}
	}
	var extra map[string]bool
	if *journeys != "" {
		if extra, err = readProfile(*journeys); err != nil {
			return err
		}
	}
	picked := subsumed(all, extra, candidateFilter(classOf, protected, classSet(*only)))
	fmt.Printf("pkg=%s tests=%d failed_alone=%d no_in_package_coverage=%d redundant_candidates=%d\n", *pkg, len(names), failed, empty, len(picked))
	if *out != "" {
		return os.WriteFile(*out, []byte(strings.Join(picked, "\n")+"\n"), 0o644)
	}
	for _, p := range picked {
		fmt.Println(p)
	}
	return nil
}

// classSet parses a comma-separated list of inventory classes.
func classSet(list string) map[Class]bool {
	set := map[Class]bool{}
	for c := range strings.SplitSeq(list, ",") {
		if c != "" {
			set[Class(c)] = true
		}
	}
	return set
}

// candidateFilter admits a test as a deletion candidate when its class is not
// protected, its name does not mark an oracle, ordering or cancellation test,
// and, when only is not empty, its class is in only.
func candidateFilter(classOf map[string]Class, protected, only map[Class]bool) func(name string) bool {
	return func(name string) bool {
		c := classOf[name]
		if len(only) > 0 && !only[c] {
			return false
		}
		return !protected[c] && !oracleNameRE.MatchString(name)
	}
}

// classesInPackage classifies the Test functions of one package directory.
func classesInPackage(root, dir string) (map[string]Class, error) {
	files, err := listTestFiles(root)
	if err != nil {
		return nil, err
	}
	var mine []string
	for _, f := range files {
		if filepath.ToSlash(filepath.Dir(f)) == dir {
			mine = append(mine, f)
		}
	}
	evidence, err := loadUnitEvidence(root)
	if err != nil {
		return nil, err
	}
	funcs, err := inventoryFiles(root, mine, evidence)
	if err != nil {
		return nil, err
	}
	classes := map[string]Class{}
	for _, f := range funcs {
		classes[f.Name] = f.Class
	}
	return classes, nil
}

// notInJourneys returns, in order, the blocks of one test that the journeys
// do not reach. Only a block the journeys executed counts: a journey profile
// also lists every block it did not reach, with a zero count.
func notInJourneys(blocks, journeys map[string]bool) []string {
	var missing []string
	for b := range blocks {
		if !journeys[b] {
			missing = append(missing, b)
		}
	}
	slices.Sort(missing)
	return missing
}

// journeyVerdict names what the journeys alone prove for one test. A test that
// covers no block of its package (a reflection or declaration guard) checks
// something block coverage cannot see, so the journeys never stand in for it,
// and neither do they for a test of a protected class or with an oracle,
// ordering or cancellation name.
func journeyVerdict(name string, class Class, blocks int, missing int, protected map[Class]bool) string {
	switch {
	case protected[class] || oracleNameRE.MatchString(name):
		return "protected"
	case blocks == 0:
		return "no_in_package_coverage"
	case missing == 0:
		return "journeys_cover_all_blocks"
	default:
		return "keeps_blocks"
	}
}

// runJourneyCover lists, for the named tests of one package, the blocks the
// end-to-end journeys do not reach. Only a test that covers blocks of its
// package, all of which the journeys reach, and that is not protected is
// covered by the journeys alone; a mutation run must still confirm a deletion.
func runJourneyCover(root string, args []string) error {
	fs := flag.NewFlagSet("journeycover", flag.ExitOnError)
	pkg := fs.String("pkg", "", "package, e.g. ./ai")
	tags := fs.String("tags", "", "build tags")
	tests := fs.String("tests", "", "comma-separated test names")
	journeys := fs.String("journeys", "", "cover profile (text format) of the journeys")
	timeout := fs.Duration("timeout", 10*time.Minute, "per test timeout")
	protect := fs.String("classes", "A-oracle,A-cited,B-invariant", "inventory classes the journeys never stand in for")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pkg == "" || *tests == "" || *journeys == "" {
		return fmt.Errorf("-pkg, -tests and -journeys are required")
	}
	extra, err := readProfile(*journeys)
	if err != nil {
		return err
	}
	classOf, err := classesInPackage(root, strings.TrimPrefix(*pkg, "./"))
	if err != nil {
		return err
	}
	protected := classSet(*protect)
	work, err := os.MkdirTemp("", "testprune-journeycover-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	cr, err := newCoverRun(root, *pkg, "", *tags, work, *timeout)
	if err != nil {
		return err
	}
	for _, t := range perTestBlocks(cr, strings.Split(*tests, ","), 2, work) {
		if t.err != nil {
			fmt.Printf("%s\terror\t%v\n", t.name, t.err)
			continue
		}
		missing := notInJourneys(t.blocks, extra)
		verdict := journeyVerdict(t.name, classOf[t.name], len(t.blocks), len(missing), protected)
		fmt.Printf("%s\t%s\t%s\tblocks=%d\tnot_in_journeys=%d\n", t.name, classOf[t.name], verdict, len(t.blocks), len(missing))
		for _, b := range missing {
			fmt.Printf("\t%s\n", b)
		}
	}
	return nil
}
