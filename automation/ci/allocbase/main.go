// SPDX-License-Identifier: MIT

// Command allocbase ratchets the allocations per operation of the repository's Go benchmarks. It runs the benchmarks of the hot packages once per sub-benchmark (-benchtime=5x, GOMAXPROCS=1, -benchmem), reads allocs/op, and compares each with automation/ci/allocbase/baseline.toml:
//   - a benchmark above its baseline by more than the tolerance fails;
//   - a benchmark below its baseline by more than the tolerance fails too, so a fix lowers its entry in the same change;
//   - a benchmark with no entry, and an entry with no benchmark, fail.
//
// Allocation counts of a benchmark that starts goroutines or servers vary by a few allocations from run to run, so the tolerance is the larger of 2 allocations and 6% (BenchmarkNativeProviderStream varies 827 to 864 between runs).
//
// Run it with `make alloc-baseline`. -update rewrites the baseline from a run, -packages limits the run.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// packages are the hot paths whose benchmarks the baseline covers.
var packages = []string{"./agent", "./ai", "./coding", "./coding/cli", "./coding/extension/host/inproc", "./coding/extension/host/subprocess", "./internal/codingagent", "./internal/sessionentry", "./tui", "./tui/widthx"}

// skipped benchmarks fail when run more than once, so they have no stable count to ratchet. Each names the reason.
var skipped = map[string]string{}

type entry struct {
	Package   string `toml:"package"`
	Benchmark string `toml:"benchmark"`
	Allocs    int64  `toml:"allocs"`
}

func (e entry) key() string { return e.Package + " " + e.Benchmark }

type baselineFile struct {
	Benchmark []entry `toml:"benchmark"`
}

// resultLine reads one result line. run sets GOMAXPROCS=1, so go test prints no -N processor suffix and a name ending in -<digits> is a sub-benchmark's own name (BenchmarkToolArgumentValidation/edits-1000), which must stay whole.
var resultLine = regexp.MustCompile(`^(Benchmark\S+)\s+\d+\s+[\d.]+ ns/op(?:\s+[\d.]+ \S+)*?\s+[\d.]+ B/op\s+(\d+) allocs/op`)

// parseResults reads `go test -bench -benchmem` output for one package.
func parseResults(pkg string, output []byte) []entry {
	var found []entry
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		if match := resultLine.FindStringSubmatch(scanner.Text()); match != nil {
			allocs, _ := strconv.ParseInt(match[2], 10, 64)
			found = append(found, entry{Package: pkg, Benchmark: match[1], Allocs: allocs})
		}
	}
	return found
}

func tolerance(baseline int64) int64 { return max(2, baseline*6/100) }

// compare lists benchmarks that rose, fell, have no entry, or lost their benchmark, and keys that appear twice in the run or the baseline.
func compare(found, baseline []entry) (problems []string) {
	recorded := map[string]entry{}
	for _, e := range baseline {
		if _, dup := recorded[e.key()]; dup {
			problems = append(problems, fmt.Sprintf("%s: more than one baseline entry", e.key()))
		}
		recorded[e.key()] = e
	}
	seen := map[string]bool{}
	for _, e := range found {
		if seen[e.key()] {
			problems = append(problems, fmt.Sprintf("%s: more than one result in the run", e.key()))
		}
		seen[e.key()] = true
		want, ok := recorded[e.key()]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %d allocs/op has no baseline entry", e.key(), e.Allocs))
		case e.Allocs > want.Allocs+tolerance(want.Allocs):
			problems = append(problems, fmt.Sprintf("%s: %d allocs/op, baseline %d", e.key(), e.Allocs, want.Allocs))
		case e.Allocs < want.Allocs-tolerance(want.Allocs):
			problems = append(problems, fmt.Sprintf("%s: %d allocs/op, baseline %d; lower the baseline entry", e.key(), e.Allocs, want.Allocs))
		}
	}
	for _, e := range baseline {
		if !seen[e.key()] {
			problems = append(problems, fmt.Sprintf("%s: in the baseline, not in the run; delete the entry", e.key()))
		}
	}
	slices.Sort(problems)
	return problems
}

func run(root string, pkgs []string, benchtime string) ([]entry, error) {
	var all []entry
	for _, pkg := range pkgs {
		args := []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "-benchtime", benchtime, "-timeout", "40m"}
		if len(skipped) > 0 {
			var names []string
			for name := range skipped {
				names = append(names, "^"+name+"$")
			}
			slices.Sort(names)
			args = append(args, "-skip", strings.Join(names, "|"))
		}
		command := exec.Command("go", append(args, pkg)...)
		command.Dir = root
		command.Env = append(os.Environ(), "GOMAXPROCS=1")
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("go test %s: %w\n%s", pkg, err, output)
		}
		all = append(all, parseResults(pkg, output)...)
	}
	slices.SortFunc(all, func(a, b entry) int { return strings.Compare(a.key(), b.key()) })
	return all, nil
}

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String("baseline", "", "baseline file (default <root>/automation/ci/allocbase/baseline.toml)")
	update := flag.Bool("update", false, "rewrite the baseline from this run")
	only := flag.String("packages", "", "comma-separated packages to run instead of the default set")
	benchtime := flag.String("benchtime", "5x", "go test -benchtime")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "alloc-baseline:", err)
		os.Exit(2)
	}
	if *baselinePath == "" {
		*baselinePath = filepath.Join(abs, "automation", "ci", "allocbase", "baseline.toml")
	}
	pkgs := packages
	if *only != "" {
		pkgs = strings.Split(*only, ",")
	}
	found, err := run(abs, pkgs, *benchtime)
	if err != nil {
		fmt.Fprintln(os.Stderr, "alloc-baseline:", err)
		os.Exit(2)
	}
	var baseline baselineFile
	if _, err := toml.DecodeFile(*baselinePath, &baseline); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "alloc-baseline:", err)
		os.Exit(2)
	}
	if *update {
		// A run limited to some packages replaces only those packages' entries.
		merged := slices.Clone(found)
		for _, e := range baseline.Benchmark {
			if !slices.Contains(pkgs, e.Package) {
				merged = append(merged, e)
			}
		}
		slices.SortFunc(merged, func(a, b entry) int { return strings.Compare(a.key(), b.key()) })
		var out strings.Builder
		// REUSE-IgnoreStart
		out.WriteString("# SPDX-License-Identifier: MIT\n#\n# Allocation ratchet (automation/ci/allocbase, `make alloc-baseline`): allocs/op of each benchmark at -benchtime=5x, GOMAXPROCS=1.\n# A fix lowers its entry in the same change; never raise one to pass. Regenerate with `go run ./automation/ci/allocbase -update` (`-packages ./ai` for one package).\n\n")
		// REUSE-IgnoreEnd
		for _, e := range merged {
			fmt.Fprintf(&out, "[[benchmark]]\npackage = %q\nbenchmark = %q\nallocs = %d\n\n", e.Package, e.Benchmark, e.Allocs)
		}
		if err := os.WriteFile(*baselinePath, []byte(out.String()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "alloc-baseline:", err)
			os.Exit(2)
		}
		fmt.Printf("alloc-baseline: wrote %d benchmarks (%d from this run)\n", len(merged), len(found))
		return
	}
	// A run limited to some packages compares only those packages' entries.
	var recorded []entry
	for _, e := range baseline.Benchmark {
		if slices.Contains(pkgs, e.Package) {
			recorded = append(recorded, e)
		}
	}
	if problems := compare(found, recorded); len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, "alloc-baseline:", problem)
		}
		os.Exit(1)
	}
	fmt.Printf("alloc-baseline: %d benchmarks within the baseline\n", len(found))
}
