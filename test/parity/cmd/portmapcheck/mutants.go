package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// mutant is a reviewed one-site edit of a cited Go file: Old occurs exactly once in File and New replaces it. A test that carries a
// `// pi:` marker proves its Pi file only when the unmutated tests pass and at least one mutant makes them fail (red-proof).
type mutant struct {
	File string `json:"file"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// onlyMarkerReason reports whether the marker is the sole reason a row is unproven.
func onlyMarkerReason(res result) bool {
	return len(res.Reasons) == 1 && strings.HasPrefix(res.Reasons[0], "MARKER-UNPROVEN")
}

// mutantProblem returns why a marker row has no killed mutant, or "". Each mutant is replayed through `go test -overlay`, so the work
// tree is never modified.
//
// The second result is non-empty when a replay could not run for a reason that says nothing about the tests (see infraFailure): the row is
// then neither proven nor MARKER-UNPROVEN, and the check fails as an infrastructure error.
func mutantProblem(root string, res result, mutants []mutant, replays *replayCaches) (why, infra string) {
	if len(mutants) == 0 {
		return "MARKER-UNPROVEN: portmap-mutants.json names no mutant of a cited file for this row", ""
	}
	cited := res.Cited
	var notes []string // what each replay that did not kill its mutant showed, so a row is never unproven without a reason
	for _, mu := range mutants {
		if !slices.Contains(cited, mu.File) {
			return "MARKER-UNPROVEN: mutant file " + mu.File + " is not a cited file", ""
		}
		if strings.HasSuffix(mu.File, "_test.go") {
			// Editing the evidence itself (an assertion turned into t.Fatal) fails the tests without touching the ported code.
			return "MARKER-UNPROVEN: mutant file " + mu.File + " is a test file; a mutant must edit the cited implementation", ""
		}
		k, infra, note := killed(root, res, mu, replays)
		if infra != "" {
			return "", "replaying the mutant of " + mu.File + ": " + infra
		}
		if k {
			return "", ""
		}
		notes = append(notes, mu.File+": "+note)
	}
	return "MARKER-UNPROVEN: no listed mutant makes the marker tests fail (the tests would pass without the cited code) [" + strings.Join(notes, "; ") + "]", ""
}

// diskMarkers are what the kernel reports when the machine failed under the build or the tests: a full, read-only or failing disk, an
// exhausted descriptor or memory limit. They are infrastructure whatever else the output holds.
var diskMarkers = []string{
	"no space left on device", "disk quota exceeded", "read-only file system", "input/output error", "too many open files",
	"cannot allocate memory",
}

// toolchainMarkers are what the Go toolchain prints when a compiler or linker died, its build cache failed, or a module download could not
// connect. They count only when no test ran: a test that ran and failed an assertion may legitimately print a dial error.
var toolchainMarkers = []string{
	"signal: killed", "internal compiler error", "failed to trim cache", "build cache is required", "dial tcp", "i/o timeout",
	"no such host", "TLS handshake timeout",
}

// infraFailure says why a go command's result is an infrastructure failure, or "" when it is a result about the code: a failing
// assertion, or a mutation that does not compile (a build error whose lines are compiler diagnostics). err is the error of the command and
// out its combined output. A command that could not start at all (no go binary, a bad working directory) is also infrastructure.
func infraFailure(out string, err error) string {
	if err == nil {
		return ""
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		return err.Error()
	}
	markers := diskMarkers
	if !strings.Contains(out, "--- FAIL") {
		markers = append(slices.Clone(diskMarkers), toolchainMarkers...)
	}
	for _, m := range markers {
		if i := strings.Index(out, m); i >= 0 {
			start := strings.LastIndex(out[:i], "\n") + 1
			end := len(out)
			if j := strings.Index(out[i:], "\n"); j >= 0 {
				end = i + j
			}
			return strings.TrimSpace(out[start:end])
		}
	}
	return ""
}

// killed replays one mutant: the old text must occur once, and the evidence tests must fail with a test failure (not a build error).
func killed(root string, res result, mu mutant, replays *replayCaches) (dead bool, infra, note string) {
	mutantReplays.Add(1)
	src, err := os.ReadFile(filepath.Join(root, mu.File))
	if err != nil {
		return false, "", "the file cannot be read: " + err.Error()
	}
	if n := strings.Count(string(src), mu.Old); n != 1 {
		return false, "", "the old text occurs " + strconv.Itoa(n) + " times, not once"
	}
	dir, err := os.MkdirTemp("", "portmap-mutant-")
	if err != nil {
		return false, err.Error(), ""
	}
	defer func() { _ = os.RemoveAll(dir) }()
	mutated := filepath.Join(dir, "mutated.go")
	if err := os.WriteFile(mutated, []byte(strings.Replace(string(src), mu.Old, mu.New, 1)), 0o644); err != nil {
		return false, err.Error(), ""
	}
	overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(root, mu.File): mutated}})
	overlayFile := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayFile, overlay, 0o644); err != nil {
		return false, err.Error(), ""
	}
	byDir := map[string][]string{}
	for _, t := range res.Tests {
		d, name, _ := strings.Cut(t, "#")
		byDir[d] = append(byDir[d], name)
	}
	for d, names := range byDir {
		slices.Sort(names)
		//nolint:gosec // G204 (class a): program is literal go; args are a temp -overlay file, -run regex built from tool-generated test names, and "./"+dir.
		cmd := exec.Command("go", "test", "-count=1", "-overlay="+overlayFile, "-run", "^("+strings.Join(names, "|")+")$", "./"+d)
		cmd.Dir = root
		cmd.Env = evidenceEnv(dir)
		cache, release := replays.acquire(cmd.Env)
		if cache != "" {
			cmd.Env = append(cmd.Env, "GOCACHE="+cache)
		}
		out, err := cmd.CombinedOutput()
		release()
		if why := infraFailure(string(out), err); why != "" {
			return false, why, ""
		}
		if err != nil && assertionFailure(string(out)) {
			return true, "", ""
		}
		note = replayNote(string(out), err)
	}
	return false, "", note
}

// replayNote says in a line why a replay did not show a failed assertion.
func replayNote(out string, err error) string {
	if err == nil {
		return "the tests passed with the mutant"
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	pick := func(match func(string) bool) string {
		for _, l := range lines {
			if match(l) {
				return strings.TrimSpace(l)
			}
		}
		return ""
	}
	switch {
	case strings.Contains(out, "[build failed]"):
		if l := pick(func(l string) bool { return strings.Contains(l, ".go:") }); l != "" {
			return "the mutant did not build: " + l
		}
		return "the mutant did not build: " + lines[0]
	case strings.Contains(out, "panic: "):
		return "the mutant panicked: " + pick(func(l string) bool { return strings.HasPrefix(l, "panic: ") })
	}
	return "the replay failed without a failed assertion: " + lines[len(lines)-1]
}

// assertionFailure reports whether go test output is a failed assertion. A build error is not a kill, and neither is a panic or a
// timeout: a mutant that panics (or loops) in the cited code fails every test that merely executes it, which rule 3's coverage
// already shows, so it proves nothing about what the tests assert.
func assertionFailure(out string) bool {
	return strings.Contains(out, "--- FAIL") && !strings.Contains(out, "[build failed]") && !strings.Contains(out, "\npanic: ") &&
		!strings.HasPrefix(out, "panic: ")
}

// replayCaches keeps the mutant replays of a shard from filling the disk. A replay recompiles the mutated package and everything that
// imports it into the Go build cache, and no later replay reads those entries: a shard with hundreds of replays would grow the shared
// cache by gigabytes nothing reuses (a GitHub runner's disk ran out that way). Replays therefore compile into one hard-linked clone of
// the cache, made when the first replay starts: it costs no space and every entry the shared cache holds is still a hit. Every
// replaysPerTrim replays, while none runs, the entries the replays added are removed from the clone, and the clone is removed at the
// end. The shared cache is never written to or trimmed, so a build running beside the check loses nothing and gains nothing.
type replayCaches struct {
	every  int
	mu     sync.RWMutex // replays hold it shared; a trim or the final removal holds it exclusively
	initMu sync.Mutex   // guards the clone's creation
	src    string
	dir    string
	failed bool // the clone could not be made: replays use the shared cache
	cnt    sync.Mutex
	done   int // replays since the last trim
}

// replaysPerTrim is how many replays may add entries to the clone before they are removed.
const replaysPerTrim = 16

func newReplayCaches() *replayCaches { return &replayCaches{every: replaysPerTrim} }

// acquire returns the GOCACHE a replay must use and the function that gives it back; "" means the shared cache (the cache is disabled,
// missing, or the filesystem refuses hard links). A nil *replayCaches always answers that.
func (c *replayCaches) acquire(env []string) (cache string, release func()) {
	none := func() {}
	if c == nil {
		return "", none
	}
	src := ""
	for _, kv := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(kv, "GOCACHE="); ok {
			src = v
			break
		}
	}
	if src == "" || src == "off" {
		return "", none
	}
	c.initMu.Lock()
	if c.dir == "" && !c.failed {
		if dir, err := cloneBuildCache(src); err != nil {
			c.failed = true
		} else {
			c.src, c.dir = src, dir
		}
	}
	dir := c.dir
	c.initMu.Unlock()
	if dir == "" {
		return "", none
	}
	c.mu.RLock()
	return dir, func() {
		c.mu.RUnlock()
		c.cnt.Lock()
		c.done++
		due := c.done >= c.every
		c.cnt.Unlock()
		if due {
			c.trim()
		}
	}
}

// trim removes from the clone every file that is not a link to the same file of the shared cache, once the replays running now have
// finished: what the replays compiled. A shared entry that was deleted since the clone was made goes too, which only costs a rebuild.
func (c *replayCaches) trim() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cnt.Lock()
	c.done = 0
	c.cnt.Unlock()
	if c.dir == "" {
		return
	}
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(c.dir, p)
		if rerr != nil {
			return nil
		}
		mine, e1 := os.Lstat(p)
		twin, e2 := os.Lstat(filepath.Join(c.src, rel))
		if e1 == nil && (e2 != nil || !os.SameFile(mine, twin)) {
			_ = os.Remove(p)
		}
		return nil
	})
}

// close removes the clone.
func (c *replayCaches) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dir != "" {
		_ = os.RemoveAll(c.dir)
		c.dir = ""
	}
}

// cloneBuildCache hard-links every file of the Go build cache at src into a new directory beside it (the same filesystem) and returns
// that directory. Cache entries are written once and never changed, so the clone and the original can share their files. It fails, and
// leaves nothing behind, when the cache is missing or the filesystem refuses the links.
func cloneBuildCache(src string) (clone string, err error) {
	if _, err := os.Stat(src); err != nil {
		return "", err
	}
	clone, err = os.MkdirTemp(filepath.Dir(src), ".portmap-gocache-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(clone)
			clone = ""
		}
	}()
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(clone, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return os.Link(p, filepath.Join(clone, rel))
	})
	return clone, err
}
