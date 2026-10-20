package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// provenanceFile declares where every golden or snapshot file's expected
// values come from.
const provenanceFile = "test/testdata-provenance.toml"

type goldenEntry struct {
	Paths []string `toml:"paths"`
	// Owner is "pi" for values produced by running Pi (or Node on Pi's code) and
	// "pig" for values PiG owns.
	Owner string `toml:"owner"`
	// Generator is the tracked script or oracle that produces a Pi golden.
	Generator string `toml:"generator"`
	// Invariant names the test that asserts the rule a PiG-owned golden pins.
	Invariant string `toml:"invariant"`
	Why       string `toml:"why"`
}

// inputEntry declares files in a golden-writing directory that are inputs, not expectations.
type inputEntry struct {
	Paths []string `toml:"paths"`
}

type provenance struct {
	// ExpectationsFrom exempts commits authored before this RFC 3339 time from the
	// expected-value rule, so history from before the ratchet is not reviewed again.
	ExpectationsFrom string        `toml:"expectations_from"`
	Golden           []goldenEntry `toml:"golden"`
	Input            []inputEntry  `toml:"input"`
}

// goldenWriterRE matches the switch a test uses to rewrite its own expected files.
var goldenWriterRE = regexp.MustCompile(`UPDATE_GOLDEN|UPDATE_SNAPSHOTS|flag\.Bool\("(?:update|golden|regen)[a-z0-9-]*"`)

// goldenWriterDirs returns the testdata directories of Go files that rewrite their own expected files.
func goldenWriterDirs(root string, tracked []string) map[string]bool {
	dirs := map[string]bool{}
	for _, f := range tracked {
		if !strings.HasSuffix(f, ".go") || path.Base(f) == "main.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || !goldenWriterRE.Match(data) {
			continue
		}
		dirs[path.Dir(f)+"/testdata/"] = true
	}
	return dirs
}

var goldenPathRE = regexp.MustCompile(`(?i)golden|\.snap$|__snapshots__|snapshot`)

var goldenSkipExt = map[string]bool{".go": true, ".mjs": true, ".js": true, ".ts": true, ".py": true, ".sh": true, ".md": true, ".rs": true, ".cjs": true}

// vendored reports whether a path is in third_party/, vendored upstream code (like node_modules):
// its fixtures are the upstream library's own, not PiG goldens.
func vendored(p string) bool { return strings.HasPrefix(p, "third_party/") }

// isGoldenFile reports whether a tracked path is a golden or snapshot data file.
func isGoldenFile(p string) bool {
	if vendored(p) || strings.Contains(p, "node_modules/") || strings.Contains(p, "/shims/") || strings.HasPrefix(p, "test/parity/scenarios/") || strings.HasPrefix(p, "docs/") {
		return false
	}
	if goldenSkipExt[path.Ext(p)] || !goldenPathRE.MatchString(p) {
		return false
	}
	return strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "testdata/") || strings.Contains(p, "__snapshots__")
}

func gitFiles(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// checkProvenance returns every violation of the golden provenance rule.
func checkProvenance(root string, tracked []string, prov provenance, hasTest func(name string) bool) []string {
	writerDirs := goldenWriterDirs(root, tracked)
	var problems []string
	exists := map[string]bool{}
	for _, f := range tracked {
		exists[f] = true
	}
	matched := map[string]bool{}
	for i, e := range prov.Golden {
		where := fmt.Sprintf("%s: golden entry %d (%v)", provenanceFile, i+1, e.Paths)
		switch e.Owner {
		case "pi":
			if e.Generator == "" {
				problems = append(problems, where+": a Pi golden names the generator that produces it from Pi")
			} else if !exists[e.Generator] {
				problems = append(problems, where+": generator "+e.Generator+" is not a tracked file")
			}
		case "pig":
			if e.Invariant == "" || e.Why == "" {
				problems = append(problems, where+": a PiG-owned golden names the invariant test that asserts its rule and why a value snapshot is needed")
			} else if !hasTest(e.Invariant) {
				problems = append(problems, where+": invariant test "+e.Invariant+" does not exist")
			}
		default:
			problems = append(problems, where+": owner must be pi or pig")
		}
		hit := false
		for _, pat := range e.Paths {
			for _, f := range tracked {
				if ok, _ := path.Match(pat, f); ok || pat == f {
					matched[f] = true
					hit = true
				}
			}
		}
		if !hit {
			problems = append(problems, where+": matches no tracked file")
		}
	}
	for _, in := range prov.Input {
		for _, pat := range in.Paths {
			for _, f := range tracked {
				if ok, _ := path.Match(pat, f); ok || pat == f {
					matched[f] = true
				}
			}
		}
	}
	inWriterDir := func(f string) bool {
		for d := range writerDirs {
			if strings.HasPrefix(f, d) && !goldenSkipExt[path.Ext(f)] {
				return true
			}
		}
		return false
	}
	for _, f := range tracked {
		if !vendored(f) && (isGoldenFile(f) || inWriterDir(f)) && !matched[f] {
			problems = append(problems, f+": golden or snapshot file without provenance in "+provenanceFile)
		}
	}
	sort.Strings(problems)
	return problems
}

var testFuncRE = regexp.MustCompile(`(?m)^func (Test\w+|Fuzz\w+)\(`)

// testNames returns every Test or Fuzz function name in the tracked test files.
func testNames(root string, tracked []string) map[string]bool {
	names := map[string]bool{}
	for _, f := range tracked {
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for _, m := range testFuncRE.FindAllSubmatch(data, -1) {
			names[string(m[1])] = true
		}
	}
	return names
}

var piCiteRE = regexp.MustCompile(`(?m)^Pi-Behavior-Change:\s*(\S*\.ts):\d+`)

// piInventories are the tracked files that name every upstream Pi source and test file by its packages/ path.
var piInventories = []string{"docs/parity/PORT_MAP.md", "test/parity/interfaces/test-mapping-v*.json", "test/parity/upstream-sync/*.toml"}

var piPathRE = regexp.MustCompile(`packages/[A-Za-z0-9_./-]+\.ts\b`)

// piSourceNames returns the file names of the Pi sources and tests the tracked inventories name. A citation counts only
// when it names one of them, so a PiG TypeScript file or an invented name does not satisfy the expected-value rule.
func piSourceNames(root string) (map[string]bool, error) {
	names := map[string]bool{}
	for _, pattern := range piInventories {
		files, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			for _, p := range piPathRE.FindAllString(string(data), -1) {
				names[path.Base(p)] = true
			}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no Pi source names in %v", piInventories)
	}
	return names, nil
}

// checkCoEdits returns each commit that edits an expectation value in a test
// file while changing production code of the same directory, unless the commit
// message or a line the commit adds to the test file cites a Pi source line
// (a file piNames holds) whose behavior changed.
func checkCoEdits(root string, rangeSpec string, from time.Time, piNames map[string]bool, ledger []reviewEntry) ([]string, error) {
	edits, err := scanCoEdits(root, []string{rangeSpec, "--", "*.go"})
	if err != nil {
		return nil, err
	}
	var problems []string
	seen := map[string]bool{}
	used := make([]bool, len(ledger))
	for _, e := range edits {
		if e.ValueEdits == 0 || seen[e.Commit+e.Path] {
			continue
		}
		if authored, err := time.Parse(time.RFC3339, e.Authored); err == nil && authored.Before(from) {
			continue
		}
		seen[e.Commit+e.Path] = true
		msg, err := exec.Command("git", "-C", root, "show", "-s", "--format=%B", e.Commit).Output()
		if err != nil {
			return nil, err
		}
		cited := slices.ContainsFunc(e.Cited, func(name string) bool { return piNames[name] })
		for _, m := range piCiteRE.FindAllSubmatch(msg, -1) {
			cited = cited || piNames[path.Base(string(m[1]))]
		}
		if cited {
			continue
		}
		reviewed := false
		for i, r := range ledger {
			if len(r.Commit) < 10 || !strings.HasPrefix(e.Commit, r.Commit) || r.Path != e.Path {
				continue
			}
			used[i] = true
			if err := r.validate(root, piNames); err != nil {
				problems = append(problems, fmt.Sprintf("%s %s: ledger entry %s", shortSHA(e.Commit), e.Path, err))
			}
			reviewed = true
		}
		if reviewed {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s %s: changes %d expected value(s) in %s together with %s; cite the Pi behavior that changed (a `Pi-Behavior-Change: <pi file.ts:line>` trailer, or a `name.ts:line` comment on a changed test line, naming a Pi file of %s), or leave the expectation alone",
			shortSHA(e.Commit), e.Subject, e.ValueEdits, e.Path, strings.Join(e.Code, ", "), piInventories[0]))
	}
	inRange, err := commitsInRange(root, rangeSpec)
	if err != nil {
		return nil, err
	}
	for i, r := range ledger {
		if !used[i] && (len(r.Commit) < 10 || entryInRange(r, inRange)) {
			problems = append(problems, fmt.Sprintf("%s: stale ledger entry %s %s matches no finding", reviewedFile, shortSHA(r.Commit), r.Path))
		}
	}
	return problems, nil
}

// commitsInRange returns the full SHA of every commit rangeSpec selects.
func commitsInRange(root, rangeSpec string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "rev-list", rangeSpec).Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-list %s: %w", rangeSpec, err)
	}
	return strings.Fields(string(out)), nil
}

func entryInRange(r reviewEntry, inRange []string) bool {
	return len(r.Commit) >= 10 && slices.ContainsFunc(inRange, func(c string) bool { return strings.HasPrefix(c, r.Commit) })
}

// prunableEntries lists the ledger entries whose commit is not in rangeSpec. A squash merge replaces the reviewed
// commit with a new one, so on main and on every later branch such an entry can never match a finding again; it
// does not fail the lint, a maintainer deletes it.
func prunableEntries(root, rangeSpec string, ledger []reviewEntry) ([]string, error) {
	inRange, err := commitsInRange(root, rangeSpec)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range ledger {
		if len(r.Commit) >= 10 && !entryInRange(r, inRange) {
			out = append(out, fmt.Sprintf("%s %s", shortSHA(r.Commit), r.Path))
		}
	}
	return out, nil
}

const reviewedFile = "test/parity/testprune-reviewed.toml"

// reviewEntry is one reviewed finding: it satisfies the expected-value finding of exactly its commit and path.
type reviewEntry struct {
	Path   string `toml:"path"`
	Commit string `toml:"commit"`
	Class  string `toml:"class"`
	Cite   string `toml:"cite"`
	Reason string `toml:"reason"`
}

var divergenceIDRE = regexp.MustCompile(`^D\d+$`)

func (r reviewEntry) validate(root string, piNames map[string]bool) error {
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("has no reason")
	}
	switch r.Class {
	case "CITE":
		m := piCiteRE.FindStringSubmatch("Pi-Behavior-Change: " + r.Cite)
		if m == nil || !piNames[path.Base(m[1])] {
			return fmt.Errorf("cite %q does not name a Pi file:line of %s", r.Cite, piInventories[0])
		}
	case "DIVERGENCE":
		if !divergenceIDRE.MatchString(r.Cite) {
			return fmt.Errorf("cite %q is not a divergence id", r.Cite)
		}
		if _, err := os.Stat(filepath.Join(root, "docs/parity/divergences", r.Cite+".md")); err != nil {
			return fmt.Errorf("divergence %s has no docs/parity/divergences/%s.md", r.Cite, r.Cite)
		}
	case "NOBEHAVIOR", "message-only":
	default:
		return fmt.Errorf("unknown class %q", r.Class)
	}
	return nil
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func runLint(root string, args []string) error {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	provOnly := fs.Bool("provenance", false, "check only golden and snapshot provenance")
	base := fs.String("base", "", "check commits in base..head for expectations edited together with code")
	head := fs.String("head", "HEAD", "range tip")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var problems []string
	if *base == "" || *provOnly {
		listed, err := gitFiles(root, "ls-files", "-z")
		if err != nil {
			return err
		}
		var tracked []string
		for _, f := range listed {
			if _, err := os.Lstat(filepath.Join(root, f)); err == nil {
				tracked = append(tracked, f)
			}
		}
		var prov provenance
		if _, err := toml.DecodeFile(filepath.Join(root, provenanceFile), &prov); err != nil {
			return err
		}
		names := testNames(root, tracked)
		problems = append(problems, checkProvenance(root, tracked, prov, func(n string) bool { return names[n] })...)
	}
	if *base != "" && !*provOnly {
		var prov provenance
		if _, err := toml.DecodeFile(filepath.Join(root, provenanceFile), &prov); err != nil {
			return err
		}
		var from time.Time
		if prov.ExpectationsFrom != "" {
			var err error
			if from, err = time.Parse(time.RFC3339, prov.ExpectationsFrom); err != nil {
				return fmt.Errorf("%s: expectations_from: %w", provenanceFile, err)
			}
		}
		piNames, err := piSourceNames(root)
		if err != nil {
			return err
		}
		var reviewed struct {
			Entry []reviewEntry `toml:"entry"`
		}
		if _, err := toml.DecodeFile(filepath.Join(root, reviewedFile), &reviewed); err != nil {
			return err
		}
		rangeSpec := *base + ".." + *head
		p, err := checkCoEdits(root, rangeSpec, from, piNames, reviewed.Entry)
		if err != nil {
			return err
		}
		prunable, err := prunableEntries(root, rangeSpec, reviewed.Entry)
		if err != nil {
			return err
		}
		for _, e := range prunable {
			fmt.Fprintf(os.Stderr, "note: %s: prunable ledger entry %s: its commit is not in %s (squash-merged or rewritten); delete it\n", reviewedFile, e, rangeSpec)
		}
		problems = append(problems, p...)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d test provenance problems", len(problems))
	}
	return nil
}
