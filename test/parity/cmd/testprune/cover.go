package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// profile maps a coverage block to whether any test executed it.
type profile map[string]bool

func readProfile(path string) (profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	p := profile{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		n, err := strconv.Atoi(line[i+1:])
		if err != nil {
			return nil, fmt.Errorf("%s: bad count in %q", path, line)
		}
		key := line[:i]
		p[key] = p[key] || n > 0
	}
	return p, sc.Err()
}

func (p profile) covered() int {
	n := 0
	for _, c := range p {
		if c {
			n++
		}
	}
	return n
}

// lostBlocks returns blocks the baseline covers and the pruned run, together
// with the extra profiles, does not.
func lostBlocks(base, pruned profile, extra ...profile) []string {
	var lost []string
	for k, c := range base {
		if !c || pruned[k] {
			continue
		}
		ok := false
		for _, e := range extra {
			if e[k] {
				ok = true
				break
			}
		}
		if !ok {
			lost = append(lost, k)
		}
	}
	sort.Strings(lost)
	return lost
}

// testEnv isolates a test binary from the operator's credentials and settings.
func testEnv(home string) []string {
	if err := os.MkdirAll(filepath.Join(home, "covdata"), 0o755); err != nil {
		return os.Environ()
	}
	env := os.Environ()
	drop := map[string]bool{"HOME": true, "PIG_CODING_AGENT_DIR": true, "PI_CODING_AGENT_DIR": true, "PIG_HOME": true, "PI_HOME": true}
	out := env[:0:0]
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if !drop[k] {
			out = append(out, e)
		}
	}
	return append(out,
		"HOME="+home,
		"PIG_CODING_AGENT_DIR="+filepath.Join(home, "pig-agent"),
		"PI_CODING_AGENT_DIR="+filepath.Join(home, "pi-agent"),
		"PIG_HOME="+filepath.Join(home, "pig-home"),
		"PI_HOME="+filepath.Join(home, "pi-home"),
		"GOCOVERDIR="+filepath.Join(home, "covdata"),
	)
}

// coverRun builds pkg's test binary instrumented for coverpkg once and runs it
// under each argument set, writing one profile per run.
type coverRun struct {
	root, pkg, bin, tags string
	timeout              time.Duration
	// allowFailing keeps the profile of a run whose tests fail instead of
	// reporting the failure, for a suite that already fails before the change.
	allowFailing bool
}

// newCoverRun builds pkg's test binary instrumented for the packages in
// coverpkg (a comma-separated list, pkg itself when empty).
func newCoverRun(root, pkg, coverpkg, tags, workDir string, timeout time.Duration) (*coverRun, error) {
	bin := filepath.Join(workDir, "pkg.test")
	if coverpkg == "" {
		coverpkg = pkg
	}
	args := []string{"test", "-c", "-covermode=set", "-coverpkg=" + coverpkg, "-o", bin}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	args = append(args, pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("build %s: %w", pkg, err)
	}
	return &coverRun{root: root, pkg: pkg, bin: bin, tags: tags, timeout: timeout}, nil
}

func (c *coverRun) run(out string, testArgs ...string) error {
	dir := filepath.Join(c.root, strings.TrimPrefix(c.pkg, "./"))
	home, err := os.MkdirTemp("", "testprune-home-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(home) }()
	args := append([]string{"-test.coverprofile=" + out, "-test.timeout=" + c.timeout.String()}, testArgs...)
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = dir
	cmd.Env = testEnv(home)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.SysProcAttr = procGroup()
	if err := cmd.Run(); err != nil {
		if _, statErr := os.Stat(out); c.allowFailing && statErr == nil {
			return nil
		}
		return fmt.Errorf("%w\n%s", err, tailLines(buf.String(), 40))
	}
	return nil
}

func runCoverDiff(root string, args []string) error {
	fs := flag.NewFlagSet("coverdiff", flag.ExitOnError)
	pkg := fs.String("pkg", "", "package under test, e.g. ./tui")
	skip := fs.String("skip", "", "regexp of tests the batch deletes (go test -skip syntax)")
	tags := fs.String("tags", "", "build tags")
	covers := fs.String("covers", "", "comma-separated packages whose coverage is measured (default: -pkg)")
	also := fs.String("also", "", "comma-separated packages whose tests also cover -pkg and stay unchanged")
	timeout := fs.Duration("timeout", 20*time.Minute, "per test run timeout")
	list := fs.Bool("list", false, "list lost blocks")
	allowFailing := fs.Bool("allow-failing", false, "measure a suite whose tests already fail (the failures are findings, not coverage)")
	against := fs.String("against", "", "checkout holding the suite before the change: compare its full run with this checkout's instead of skipping tests")
	lostOut := fs.String("lost-out", "", "write the lost blocks to this file")
	deletedOut := fs.String("deleted-out", "", "write the blocks the skipped tests cover to this file (the mutation targets)")
	require := fs.String("require", "", "instead of comparing runs, require every block listed in this file to be covered by the full run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	modes := 0
	for _, set := range []bool{*skip != "", *require != "", *against != ""} {
		if set {
			modes++
		}
	}
	if *pkg == "" || modes != 1 {
		return fmt.Errorf("-pkg and exactly one of -skip, -require or -against are required")
	}
	work, err := os.MkdirTemp("", "testprune-cover-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if err := os.MkdirAll(filepath.Join(work, "before"), 0o755); err != nil {
		return err
	}
	cr, err := newCoverRun(root, *pkg, *covers, *tags, work, *timeout)
	if err != nil {
		return err
	}
	if *require != "" {
		return requireBlocks(cr, work, *require)
	}
	cr.allowFailing = *allowFailing
	baseOut, prunedOut := filepath.Join(work, "base.out"), filepath.Join(work, "pruned.out")
	if *against != "" {
		before, err := newCoverRun(*against, *pkg, *covers, *tags, filepath.Join(work, "before"), *timeout)
		if err != nil {
			return err
		}
		before.allowFailing = *allowFailing
		if err := before.run(baseOut); err != nil {
			return fmt.Errorf("run of %s before the change failed: %w", *pkg, err)
		}
		if err := cr.run(prunedOut); err != nil {
			return fmt.Errorf("run of %s after the change failed: %w", *pkg, err)
		}
	} else {
		if err := cr.run(baseOut); err != nil {
			return fmt.Errorf("baseline run of %s failed: %w", *pkg, err)
		}
		if err := cr.run(prunedOut, "-test.skip="+*skip); err != nil {
			return fmt.Errorf("pruned run of %s failed: %w", *pkg, err)
		}
	}
	if *deletedOut != "" {
		onlyOut := filepath.Join(work, "only.out")
		if err := cr.run(onlyOut, "-test.run="+*skip); err != nil {
			return fmt.Errorf("run of the skipped tests failed: %w", err)
		}
		only, err := readProfile(onlyOut)
		if err != nil {
			return err
		}
		var covered []string
		for k, c := range only {
			if c {
				covered = append(covered, k)
			}
		}
		sort.Strings(covered)
		if err := os.WriteFile(*deletedOut, []byte(strings.Join(covered, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	base, err := readProfile(baseOut)
	if err != nil {
		return err
	}
	pruned, err := readProfile(prunedOut)
	if err != nil {
		return err
	}
	var extras []profile
	for i, other := range strings.Split(*also, ",") {
		if other == "" {
			continue
		}
		out := filepath.Join(work, fmt.Sprintf("also%d.out", i))
		dir := filepath.Join(root, strings.TrimPrefix(other, "./"))
		measured := *covers
		if measured == "" {
			measured = *pkg
		}
		cmd := exec.Command("go", "test", "-cover", "-coverpkg="+measured, "-coverprofile="+out, "-covermode=set", other)
		cmd.Dir = root
		cmd.Env = testEnv(work)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("run %s (%s): %w", other, dir, err)
		}
		p, err := readProfile(out)
		if err != nil {
			return err
		}
		extras = append(extras, p)
	}
	lost := lostBlocks(base, pruned, extras...)
	fmt.Printf("pkg=%s skip=%q against=%q baseline_covered=%d pruned_covered=%d lost=%d\n", *pkg, *skip, *against, base.covered(), pruned.covered(), len(lost))
	if *lostOut != "" {
		if err := os.WriteFile(*lostOut, []byte(strings.Join(lost, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	if *list || len(lost) > 0 {
		for _, l := range lost {
			fmt.Println("lost", l)
		}
	}
	if len(lost) > 0 {
		return fmt.Errorf("%d blocks lose all coverage", len(lost))
	}
	return nil
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// requireBlocks runs the full suite and fails when a block named in file is uncovered.
func requireBlocks(cr *coverRun, work, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	out := filepath.Join(work, "require.out")
	if err := cr.run(out); err != nil {
		return err
	}
	p, err := readProfile(out)
	if err != nil {
		return err
	}
	var missing []string
	n := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}
		n++
		if !p[line] {
			missing = append(missing, line)
		}
	}
	fmt.Printf("required=%d uncovered=%d\n", n, len(missing))
	for _, m := range missing {
		fmt.Println("uncovered", m)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d required blocks are not covered", len(missing))
	}
	return nil
}
