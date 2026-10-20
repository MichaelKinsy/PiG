package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// evidenceBuilds counts the test binaries built and mutantReplays the mutants replayed; the tests read them to see what a check shared
// or reused.
var evidenceBuilds, mutantReplays atomic.Int64

// A row's evidence requirement is unchanged by how the runs are organised: every evidence test the row names passes, the tests execute a
// statement of each mapped cited file, and a marker row's reviewed mutant makes them fail. What this file shares between rows is the work
// around the tests, not any verdict:
//
//   - A package's test binary is compiled and linked once, with every package its rows cite instrumented, instead of once per row (a
//     package such as internal/codingagent carries dozens of rows).
//   - Rows that name the same tests of a package share one run of them.
//   - A green record is cached by the content digest of the row's cited files, evidence packages and mutants, so only a changed row runs
//     again; a red run is never cached.
//
// Each run is `<binary> -test.run ^(names)$`, the same selection and the same coverage profile `go test -run -coverprofile` produced.

// evidenceTimeout is go test's default per-binary timeout, which the test binaries previously ran under.
const evidenceTimeout = "10m0s"

// cachedEntry returns the green record cached under digest, if any.
func cachedEntry(root, digest string) (coverageEntry, bool) {
	b, err := os.ReadFile(filepath.Join(root, "build", "portmap-coverage", digest+".json"))
	if err != nil {
		return coverageEntry{}, false
	}
	var e coverageEntry
	if json.Unmarshal(b, &e) != nil || e.Digest != digest {
		return coverageEntry{}, false
	}
	return e, true
}

// storeEntry caches a green record. A red run is never cached: a failure can come from the environment (a missing Node dependency of a
// Pi-oracle test, a toolchain), which the digest of the cited files and tests cannot see, so the next check runs the tests again.
func storeEntry(root string, e coverageEntry) {
	if e.Failed != "" {
		return
	}
	dir := filepath.Join(root, "build", "portmap-coverage")
	if b, err := json.Marshal(e); err == nil && os.MkdirAll(dir, 0o755) == nil {
		_ = os.WriteFile(filepath.Join(dir, e.Digest+".json"), b, 0o644)
	}
}

// evidenceUnit is one proven row awaiting its coverage verdict.
type evidenceUnit struct {
	row    int
	digest string
	entry  coverageEntry
	cached bool
}

// proveByEvidence applies rule 3 (and the red-proof of a marker row) to every proven row of this shard: a row stays proven only with a
// green record that executes its mapped cited files and, for a marker row, a killed mutant.
func proveByEvidence(root string, res []result, mutants map[string][]mutant) {
	started := time.Now()
	dg := newDigester(root)
	var units []*evidenceUnit
	for i := range res {
		if res[i].Skipped || !res[i].Proven {
			continue
		}
		u := &evidenceUnit{row: i, digest: dg.of(res[i], mutants[res[i].Row.Pi])}
		u.entry, u.cached = cachedEntry(root, u.digest)
		units = append(units, u)
	}
	stats := runMissing(root, res, units)

	// The verdict of each row, and the mutant replays of the marker rows, run under the same worker limit as the evidence runs.
	slots := make(chan struct{}, workers())
	var wg sync.WaitGroup
	var mutantTime time.Duration
	var mu sync.Mutex
	for _, u := range units {
		wg.Go(func() {
			r := &res[u.row]
			if why := coverageProblem(root, *r, u.entry); why != "" {
				r.Proven = false
				r.Reasons = append(r.Reasons, why)
				return
			}
			if !r.Marker || u.entry.Killed {
				return
			}
			slots <- struct{}{}
			t0 := time.Now()
			why := mutantProblem(root, *r, mutants[r.Row.Pi])
			<-slots
			mu.Lock()
			mutantTime += time.Since(t0)
			mu.Unlock()
			if why != "" {
				r.Proven = false
				r.Reasons = append(r.Reasons, why)
				return
			}
			u.entry.Killed = true
			storeEntry(root, u.entry)
		})
	}
	wg.Wait()
	cached := 0
	for _, u := range units {
		if u.cached {
			cached++
		}
	}
	fmt.Fprintf(os.Stderr, "portmap: %d rows: %d cached, %d run; %d test binaries built (%s), %d evidence runs (%s), mutant replays %s; %s wall\n",
		len(units), cached, len(units)-cached, stats.builds, stats.buildTime.Round(time.Second), stats.runs, stats.runTime.Round(time.Second),
		mutantTime.Round(time.Second), time.Since(started).Round(time.Second))
	for _, s := range stats.slowest(5) {
		fmt.Fprintf(os.Stderr, "portmap:   slow %s\n", s)
	}
	for _, s := range stats.heaviestDirs(8) {
		fmt.Fprintf(os.Stderr, "portmap:   package %s\n", s)
	}
}

func workers() int { return max(1, runtime.NumCPU()/2) }

// group is one run of a set of tests of one package; every row that names exactly these tests there shares its result.
type group struct {
	dir     string
	names   []string
	failed  bool
	covered map[string]int
	took    time.Duration
}

// dirJob is the test binary of one evidence package and the runs it serves.
type dirJob struct {
	dir    string
	pkgs   map[string]bool
	groups map[string]*group
	weight int
}

type evidenceStats struct {
	builds, runs int
	buildTime    time.Duration
	runTime      time.Duration
	events       []string
	times        []time.Duration
	// perDir is the time each evidence package's builds and runs took, to see which package bounds a shard.
	perDir map[string]time.Duration
}

// heaviestDirs names the n evidence packages that took longest to build and run.
func (s *evidenceStats) heaviestDirs(n int) []string {
	dirs := make([]string, 0, len(s.perDir))
	for d := range s.perDir {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(a, b int) bool { return s.perDir[dirs[a]] > s.perDir[dirs[b]] })
	var out []string
	for _, d := range dirs[:min(n, len(dirs))] {
		out = append(out, fmt.Sprintf("%s %s", s.perDir[d].Round(time.Second), d))
	}
	return out
}

func (s *evidenceStats) slowest(n int) []string {
	idx := make([]int, len(s.events))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return s.times[idx[a]] > s.times[idx[b]] })
	var out []string
	for _, i := range idx[:min(n, len(idx))] {
		out = append(out, fmt.Sprintf("%s %s", s.times[i].Round(100*time.Millisecond), s.events[i]))
	}
	return out
}

// runMissing runs every group of tests the rows without a cached record need, fills each such unit's entry, and caches the green ones.
func runMissing(root string, res []result, units []*evidenceUnit) *evidenceStats {
	stats := &evidenceStats{perDir: map[string]time.Duration{}}
	jobs := map[string]*dirJob{}
	type need struct {
		u     *evidenceUnit
		byDir map[string][]string
	}
	var needs []need
	for _, u := range units {
		if u.cached {
			continue
		}
		byDir := map[string][]string{}
		for _, t := range res[u.row].Tests {
			dir, name, _ := strings.Cut(t, "#")
			byDir[dir] = append(byDir[dir], name)
		}
		needs = append(needs, need{u, byDir})
		for dir, names := range byDir {
			slices.Sort(names)
			job := jobs[dir]
			if job == nil {
				job = &dirJob{dir: dir, pkgs: map[string]bool{}, groups: map[string]*group{}}
				jobs[dir] = job
			}
			for _, f := range coverageFiles(res[u.row]) {
				job.pkgs["./"+path.Dir(f)] = true
			}
			key := strings.Join(names, "|")
			if job.groups[key] == nil {
				job.groups[key] = &group{dir: dir, names: names}
				job.weight += len(names)
			}
		}
	}
	list := make([]*dirJob, 0, len(jobs))
	for _, j := range jobs {
		list = append(list, j)
	}
	// The heaviest package first, so the longest runs do not start last.
	sort.Slice(list, func(a, b int) bool {
		if list[a].weight != list[b].weight {
			return list[a].weight > list[b].weight
		}
		return list[a].dir < list[b].dir
	})
	var mu sync.Mutex
	note := func(kind, dir, what string, d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		stats.perDir[dir] += d
		stats.events = append(stats.events, kind+" "+what)
		stats.times = append(stats.times, d)
		if kind == "build" {
			stats.builds++
			stats.buildTime += d
		} else {
			stats.runs++
			stats.runTime += d
		}
	}
	slots := make(chan struct{}, workers())
	open := make(chan struct{}, workers()) // packages whose test binary is on disk
	var wg sync.WaitGroup
	for _, job := range list {
		open <- struct{}{}
		wg.Go(func() {
			defer func() { <-open }()
			scratch, err := os.MkdirTemp("", "portmap-bin-")
			if err != nil {
				failGroups(job)
				return
			}
			defer func() { _ = os.RemoveAll(scratch) }()
			bin := filepath.Join(scratch, "evidence.test")
			slots <- struct{}{}
			t0 := time.Now()
			ok := buildTestBinary(root, job, bin)
			<-slots
			note("build", job.dir, job.dir, time.Since(t0))
			if !ok {
				failGroups(job)
				return
			}
			var runs sync.WaitGroup
			for _, g := range job.groups {
				runs.Go(func() {
					slots <- struct{}{}
					defer func() { <-slots }()
					t0 := time.Now()
					runGroup(root, bin, scratch, g)
					g.took = time.Since(t0)
					note("run", g.dir, g.dir+" ("+strconv.Itoa(len(g.names))+" tests)", g.took)
				})
			}
			runs.Wait()
		})
	}
	wg.Wait()
	for _, n := range needs {
		e := coverageEntry{Digest: n.u.digest, Covered: map[string]int{}}
		files := coverageFiles(res[n.u.row])
		for dir, names := range n.byDir {
			g := jobs[dir].groups[strings.Join(names, "|")]
			if g.failed {
				e.Failed = dir
				continue
			}
			for _, f := range files {
				e.Covered[f] += g.covered[f]
			}
		}
		n.u.entry = e
		storeEntry(root, e)
	}
	return stats
}

func failGroups(job *dirJob) {
	for _, g := range job.groups {
		g.failed = true
	}
}

// buildTestBinary compiles the evidence package's tests with every cited package of its rows instrumented.
func buildTestBinary(root string, job *dirJob, bin string) bool {
	pkgs := make([]string, 0, len(job.pkgs))
	for p := range job.pkgs {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	evidenceBuilds.Add(1)
	cmd := exec.Command("go", "test", "-c", "-coverpkg="+strings.Join(pkgs, ","), "-o", bin, "./"+job.dir)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "portmap: building the evidence tests of %s failed: %v\n%s\n", job.dir, err, lastLines(out, 40))
		return false
	}
	if _, err := os.Stat(bin); err != nil {
		fmt.Fprintf(os.Stderr, "portmap: %s has no test files\n", job.dir)
		return false
	}
	return true
}

// runGroup runs the named tests of one package and records the statements they executed in each instrumented file.
func runGroup(root, bin, scratchRoot string, g *group) {
	scratch, err := os.MkdirTemp(scratchRoot, "agent-")
	if err != nil {
		g.failed = true
		return
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	profile := filepath.Join(scratch, "cover.out")
	// go test hands a coverage-instrumented binary a directory for its coverage data, as a flag and as GOCOVERDIR, which the test's own
	// child processes (a test that re-runs its binary) inherit. Without it such a child warns on stderr that no coverage data is emitted.
	coverDir := filepath.Join(scratch, "gocoverdir")
	if err := os.Mkdir(coverDir, 0o755); err != nil {
		g.failed = true
		return
	}
	cmd := exec.Command(bin, "-test.count=1", "-test.timeout="+evidenceTimeout, "-test.paniconexit0", "-test.gocoverdir="+coverDir,
		"-test.run=^("+strings.Join(g.names, "|")+")$", "-test.coverprofile="+profile)
	cmd.Dir = filepath.Join(root, g.dir)
	// go test also points PWD at the package directory the binary runs in.
	cmd.Env = append(evidenceEnv(scratch), "GOCOVERDIR="+coverDir, "PWD="+cmd.Dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "portmap: evidence tests of %s failed: %v\n%s\n", g.dir, err, lastLines(out, 40))
		g.failed = true
		return
	}
	prof, err := os.ReadFile(profile)
	if err != nil {
		g.failed = true
		return
	}
	g.covered = map[string]int{}
	module := modulePath(root)
	for line := range strings.SplitSeq(string(prof), "\n") {
		file, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 3 {
			continue
		}
		stmts, _ := strconv.Atoi(fields[1])
		count, _ := strconv.Atoi(fields[2])
		if count > 0 {
			g.covered[strings.TrimPrefix(file, module+"/")] += stmts
		}
	}
}
