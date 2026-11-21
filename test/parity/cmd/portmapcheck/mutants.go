package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
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
//
// The third result is what the replay that killed the mutant showed besides the failed assertions (the test binary also crashed), or "":
// the kill stands, and the caller reports it beside the verdict.
func mutantProblem(root string, res result, mutants []mutant, replays *replayCaches, dirTook map[string]time.Duration) (why, infra, alsoShowed string) {
	if len(mutants) == 0 {
		return "MARKER-UNPROVEN: portmap-mutants.json names no mutant of a cited file for this row", "", ""
	}
	cited := res.Cited
	var notes []string // what each replay that did not kill its mutant showed, so a row is never unproven without a reason
	for _, mu := range mutants {
		if !slices.Contains(cited, mu.File) {
			return "MARKER-UNPROVEN: mutant file " + mu.File + " is not a cited file", "", ""
		}
		if strings.HasSuffix(mu.File, "_test.go") {
			// Editing the evidence itself (an assertion turned into t.Fatal) fails the tests without touching the ported code.
			return "MARKER-UNPROVEN: mutant file " + mu.File + " is a test file; a mutant must edit the cited implementation", "", ""
		}
		k, infra, note := killed(root, res, mu, replays, dirTook)
		if infra != "" {
			return "", "replaying the mutant of " + mu.File + ": " + infra, ""
		}
		if k {
			if note != "" {
				note = mu.File + ": " + note
			}
			return "", "", note
		}
		notes = append(notes, mu.File+": "+note)
	}
	return "MARKER-UNPROVEN: no listed mutant makes the marker tests fail by an assertion [" + strings.Join(notes, "; ") + "]", "", ""
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
	"no such host", "TLS handshake timeout", "GOCACHEPROG",
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

// killed replays one mutant: the old text must occur once, and a marker test must fail by an assertion (see classifyReplay). A replay
// that killed the mutant and also crashed the test binary still kills it: the crash is returned in note, to be reported beside the verdict.
func killed(root string, res result, mu mutant, replays *replayCaches, dirTook map[string]time.Duration) (dead bool, infra, note string) {
	mutantReplays.Add(1)
	// `go test -overlay` keys Replace entries on the canonical path of the working directory; resolve symlinks
	// in root once so overlays apply when the repository root lies behind a symbolic link (e.g. macOS /tmp).
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
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
		cmd := replayCommand(root, d, overlayFile, names, dirTook[d])
		cmd.Env = evidenceEnv(dir)
		add, release, err := replays.acquire(cmd.Env)
		if err != nil {
			return false, "preparing the replay's build cache: " + err.Error(), ""
		}
		cmd.Env = append(cmd.Env, add...)
		out, err := cmd.CombinedOutput()
		release()
		if why := infraFailure(string(out), err); why != "" {
			return false, why, ""
		}
		verdict := classifyReplay(string(out), err)
		if verdict.killed {
			return true, "", verdict.note
		}
		note = verdict.note
	}
	return false, "", note
}

// replayVerdict is what one replay of a mutant showed.
type replayVerdict struct {
	// killed is whether a marker test failed by an assertion.
	killed bool
	// note says in a line why the replay is not a kill; for a kill it is what the replay also showed (the test binary crashed after
	// the failed assertions), or "".
	note string
}

// classifyReplay classifies the output and error of one `go test` replay of a mutant. Only a failed assertion proves a marker row: a
// mutant that panics, loops or does not compile fails every test that merely executes the cited code, which the coverage rule already
// shows, so it says nothing about what the tests assert. The classes, in this order:
//
//   - the tests passed: the mutant survives;
//   - the mutant did not build;
//   - at least one marker test failed by an assertion: killed, whatever else the replay showed. A crash after the failed assertions
//     (a race the mutant provokes in a later test) does not undo them: it is returned in the note, so it is reported beside the kill;
//   - the tests timed out, or the test binary panicked, and no test had failed by an assertion: not a kill, each with its own note;
//   - anything else (the process was killed, a test exited): not a kill.
func classifyReplay(out string, err error) replayVerdict {
	if err == nil {
		return replayVerdict{note: "the tests passed with the mutant"}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	crash := crashSite(out)
	switch {
	case strings.Contains(out, "[build failed]"):
		for _, l := range lines {
			if strings.Contains(l, ".go:") {
				return replayVerdict{note: "the mutant did not build: " + strings.TrimSpace(l)}
			}
		}
		return replayVerdict{note: "the mutant did not build: " + lines[0]}
	case len(failedAssertions(out)) > 0:
		if strings.Contains(out, "panic: test timed out") {
			return replayVerdict{killed: true, note: "the replay hit its timeout after failed assertions: " + crash}
		}
		if crash != "" {
			return replayVerdict{killed: true, note: "the mutant also panicked: " + crash}
		}
		return replayVerdict{killed: true}
	case strings.Contains(out, "panic: test timed out"):
		return replayVerdict{note: "the mutant made the tests time out and no test failed by an assertion: " + crash}
	case crash != "":
		return replayVerdict{note: "the mutant panicked: " + crash + "; no test failed by an assertion first"}
	}
	return replayVerdict{note: "the replay failed without a failed assertion: " + lines[len(lines)-1]}
}

var (
	// failHeaderRe is the line go test prints for a failed test or subtest: its indentation, then its name.
	failHeaderRe = regexp.MustCompile(`^(\s*)--- FAIL: (\S+) `)
	// failMessageRe is a line a test reported with t.Error, t.Fatal or t.Log: file.go:12: text.
	failMessageRe = regexp.MustCompile(`^\s+\S+\.go:\d+: `)
	// stackFileRe is the file line of a stack frame: a tab, the path, the line and the offset.
	stackFileRe = regexp.MustCompile(`^\t(\S+\.go:\d+) \+0x`)
	// runtimeFrame is a frame of the Go runtime or the test machinery, which every panic of a test passes through.
	runtimeFrame = regexp.MustCompile(`/src/(runtime|testing|sync|internal|iter)/`)
)

// testRunnerFrame is the frame a panic of a test's own goroutine passes through: testing.tRunner recovers it, prints the header of the
// test with the lines it reported, and panics again ("[recovered, repanicked]").
const testRunnerFrame = "testing.tRunner.func1"

// failedAssertions lists the tests that failed by an assertion: a failure header with a line the test reported below it, which is how
// go test prints a failed test. A test whose own goroutine panicked is not listed, whatever it reported before: its failure is the
// crash, which proves nothing about what the tests assert, and neither is any test that only failed because it contains the panicking
// subtest (its parents). The tests that finished before it, siblings included, keep their failures: TestA/first that failed by an
// assertion stays one when TestA/second panics. go test prints the header of the panicking test, with the lines it reported, just
// before the panic line, and the panic's stack holds the test runner's frame; a panic of another goroutine, or the test timeout, has
// no header of its own, so every failed assertion printed before it stands.
func failedAssertions(out string) []string {
	type header struct {
		name     string
		indent   int // the depth of the test: its subtests are indented deeper than it
		reported bool
		crashed  bool // the panicking test, or a test that contains it
	}
	var headers []header
	current := -1 // the header the next message belongs to
	lines := strings.Split(out, "\n")
loop:
	for i, line := range lines {
		switch {
		case failHeaderRe.MatchString(line):
			m := failHeaderRe.FindStringSubmatch(line)
			headers = append(headers, header{name: m[2], indent: len(m[1])})
			current = len(headers) - 1
		case current >= 0 && failMessageRe.MatchString(line):
			headers[current].reported = true
			current = -1
		case line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			current = -1 // an unindented line (the panic, FAIL, ok) ends what the header reported
			if strings.HasPrefix(line, "panic: ") {
				if len(headers) > 0 && strings.Contains(strings.Join(lines[i+1:], "\n"), testRunnerFrame) {
					depth := headers[len(headers)-1].indent + 1
					for j := len(headers) - 1; j >= 0; j-- {
						if headers[j].indent < depth {
							headers[j].crashed = true
							depth = headers[j].indent
						}
					}
				}
				break loop
			}
		}
	}
	var failed []string
	for _, h := range headers {
		if h.reported && !h.crashed {
			failed = append(failed, h.name)
		}
	}
	return failed
}

// crashSite is the first `panic: ` line of go test output and, when the stack follows, the first frame outside the Go runtime and the
// test machinery (where the code under test panicked), or "" when the output holds no panic.
func crashSite(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "panic: ") {
			continue
		}
		site := strings.TrimSpace(line)
		for _, frame := range lines[i+1:] {
			if m := stackFileRe.FindStringSubmatch(frame); m != nil && !runtimeFrame.MatchString(m[1]) {
				return site + " at " + m[1]
			}
		}
		return site
	}
	return ""
}

const (
	// replayTimeoutDefault matches go test's 10-minute default as the floor for every mutant replay (including unmeasured cached runs).
	// Because a bare timeout does not kill a mutant, keeping the floor at 10 minutes prevents a replay panic from racing a test's own
	// wait budget before its failing assertion runs.
	replayTimeoutDefault = 10 * time.Minute
	replayTimeoutFactor  = 4
)

// replayTimeout derives the `-timeout` budget for a mutant replay from the unmutated evidence run's duration:
// max(replayTimeoutDefault, replayTimeoutFactor*took), capped at evidenceTimeoutDuration.
func replayTimeout(took time.Duration) time.Duration {
	d := took * replayTimeoutFactor
	if d < replayTimeoutDefault {
		return replayTimeoutDefault
	}
	if d > evidenceTimeoutDuration {
		return evidenceTimeoutDuration
	}
	return d.Round(time.Second)
}

// replayCommand is the `go test` of one replay of a mutant: the named tests of package directory d (relative to root), compiled with
// the overlay and bounded by a -timeout derived from unmutatedTook. classifyReplay reads the plain output of go test, where a failed
// test's header comes before the lines it reported, so the command line fixes the two flags that change that shape and the caller's
// GOFLAGS, which go test reads first, must not: -v=true prints every reported line as it happens, before the "--- FAIL:" header, and
// -json prints no text at all.
func replayCommand(root, d, overlayFile string, names []string, unmutatedTook time.Duration) *exec.Cmd {
	timeout := replayTimeout(unmutatedTook).String()
	//nolint:gosec // G204 (class a): program is literal go; args are literal flags, a temp -overlay file, -run regex built from tool-generated test names, and "./"+dir.
	cmd := exec.Command("go", "test", "-count=1", "-timeout="+timeout, "-v=false", "-json=false", "-overlay="+overlayFile, "-run", "^("+strings.Join(names, "|")+")$", "./"+d)
	cmd.Dir = root
	return cmd
}
