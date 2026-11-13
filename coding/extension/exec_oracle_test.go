package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type execScenario struct {
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	CWD       string   `json:"cwd"`
	Timeout   *float64 `json:"timeout,omitempty"`
	MaxBuffer *float64 `json:"maxBuffer,omitempty"`
	Aborted   bool     `json:"aborted,omitempty"`
}

type execOutcome struct {
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Code      int    `json:"code"`
	Killed    bool   `json:"killed"`
	Truncated bool   `json:"truncated"`
	Thrown    string `json:"thrown,omitempty"`
}

func execScenarios(dir string) []execScenario {
	rng := rand.New(rand.NewPCG(31, 7))
	sh := func(script string) []string { return []string{"-c", script} }
	scripts := []string{"echo out; echo err >&2; exit 3", "exit 0", "exit 255", "exit 256", "echo -n no-newline", "echo 日本語 ✓; echo 😀 >&2", "printf '\\377\\376abc'", "kill -9 $$", "kill -TERM $$", "kill -HUP $$", "echo $0", "pwd", "head -c 300000 /dev/zero | tr '\\0' x", "echo start; sleep 1 & echo background", "cat; echo after-cat", "printf 'a\\nb\\n'; printf 'c' >&2", "ls /nonexistent-path-for-oracle", "true", "false", "case $$ in *[!0-9]*) echo not-numeric;; *) echo numeric-pid;; esac"}
	var scenarios []execScenario
	for _, script := range scripts {
		scenarios = append(scenarios, execScenario{Command: "sh", Args: sh(script), CWD: dir})
	}
	scenarios = append(scenarios, execTimerScenarios(dir)...)
	// No timer fires: a timeout of zero or below starts none, and one of 1e9 ms is eleven days away. Each script runs to its end.
	for _, timeout := range []float64{0, -5, 1e9} {
		for _, script := range []string{"sleep 2; echo late", "echo quick", "echo before; sleep 3", "trap '' TERM; sleep 2; echo trapped"} {
			t := timeout
			scenarios = append(scenarios, execScenario{Command: "sh", Args: sh(script), CWD: dir, Timeout: &t})
		}
	}
	scenarios = append(scenarios, []execScenario{
		{Command: "sleep", Args: []string{"3"}, CWD: dir, Aborted: true},
		{Command: "echo", Args: []string{"aborted before start"}, CWD: dir, Aborted: true},
		{Command: "echo", Args: []string{"a", "b c", "", "d\te", "日本語", "$HOME", "*"}, CWD: dir},
		{Command: "echo", Args: nil, CWD: dir},
		{Command: "pwd", CWD: dir},
		{Command: "pwd", CWD: "/nonexistent-dir-for-oracle"},
		{Command: "definitely-not-a-program", Args: []string{"x"}, CWD: dir},
		{Command: "/nonexistent/absolute/program", CWD: dir},
		{Command: "", CWD: dir},
		{Command: "echo\x00bad", CWD: dir},
		{Command: "echo", Args: []string{"nul\x00arg"}, CWD: dir},
		{Command: "echo", Args: []string{"x"}, CWD: "bad\x00dir"},
		{Command: dir, CWD: dir},
		{Command: "sh", Args: []string{"-c", "exit 7"}, CWD: dir},
	}...)
	// maxBuffer: caps below, at and above the output, caps that split a character (e2 82 ac, f0 9f 99 82), a flood that only the cap ends, a zero cap, and values execCommand rejects.
	// The code of a truncated result is a race in Pi itself: exec.ts closes both pipes and then sends SIGTERM (exec.ts:111-113), and by then the command
	// has exited (its own code, 1 for a 0), or it dies of the SIGPIPE its next write meets (141) or of the SIGTERM (143). The printf rows end by
	// themselves and report 1 unless the SIGTERM is quicker than their exit; `exec yes` makes the endless writer the command itself; `yes; echo done`
	// leaves sh waiting for it, to die of the SIGTERM or, once yes is gone, of the SIGPIPE its own echo meets. TestExecCommandMatchesPi counts the codes
	// of these rows over execTruncatedRuns runs on each side (truncatedCodeFaults).
	for _, script := range []string{"printf 'a€🙂'", "printf 'a€🙂' >&2", "printf 'ab' ; printf 'cd' >&2", "printf '\\342'; printf '\\360' >&2", "printf '\\342\\202'", "exec yes", "exec yes >&2", "yes; echo done", "printf x"} {
		limits := []float64{0, 1, 2, 3, 4, 5, 8, 9, 1000, 536870888}
		if strings.Contains(script, "yes") {
			// The cap alone ends an endless writer.
			limits = limits[:len(limits)-1]
		}
		for _, limit := range limits {
			l := limit
			scenarios = append(scenarios, execScenario{Command: "sh", Args: sh(script), CWD: dir, MaxBuffer: &l})
		}
	}
	for _, limit := range []float64{-1, 0.5, 536870889, 1e300} {
		l := limit
		scenarios = append(scenarios, execScenario{Command: "pi-command-that-does-not-exist-11360", CWD: dir, MaxBuffer: &l})
	}
	exits := []int{0, 1, 2, 42, 127, 128, 130, 255}
	for range 60 {
		script := "echo " + strings.Repeat("x", rng.IntN(50)) + "; echo e >&2; exit " + []string{"0", "1", "2", "42", "127", "130", "255"}[rng.IntN(len(exits)-1)]
		scenarios = append(scenarios, execScenario{Command: "sh", Args: sh(script), CWD: dir})
	}
	return scenarios
}

// execTimerScenarios are the scenarios whose result depends on a timer that fires or does not fire. Pi's result is the race between the timer and the
// script, so each row leaves far more than a stall between them: sh can take hundreds of milliseconds to start and run its first command (bash
// started as "sh" also searches the PATH for itself, and Git for Windows' sh.exe starts a second bash), and a host that keeps the process off the CPU
// for seconds lets a short timer expire and a short script finish before the process runs again. exec.ts appends output until the pipes end or fall
// idle, not until the kill, so a call that wakes after such a stall reads what the script wrote meanwhile, in Pi as in PiG: `sleep 2; echo late` with
// a 1000 ms timer resolves "late\n" killed, where a prompt run resolves no output, and `echo quick` with the same timer resolves killed, where a
// prompt run does not. TestExecOracleTimerScenariosHoldWhileTheCallerStalls freezes the caller across these rows.
//
// A long sleep is `exec sleep 60`: the sleep is the process the timer ends, so no descendant is left to outlive the test run (the CI reaper fails a
// group that leaves one).
//
//   - A timer that ends the shell during a sleep that outlasts any stall, before the shell writes anything: killed, no output.
//   - A timer that is further away than a command takes: not killed.
//   - A timer that ends the shell after its first output: killed, the output kept.
//   - A timer that SIGTERM does not obey: the shell goes on, and its end, four seconds in, comes before the SIGKILL that follows five seconds after the
//     timer, so the output is kept whenever the call wakes. Windows has no SIGTERM to obey: its kill ends the shell, so there the sleep outlasts a stall.
func execTimerScenarios(dir string) []execScenario {
	trapped := "trap '' TERM; sleep 4; echo trapped"
	if runtime.GOOS == "windows" {
		trapped = "trap '' TERM; exec sleep 60"
	}
	var scenarios []execScenario
	for _, row := range []struct {
		timeout float64
		script  string
	}{
		{1000, "exec sleep 60"},
		{60000, "echo quick"},
		{3000, "echo before; exec sleep 60"},
		{1000, trapped},
	} {
		timeout := row.timeout
		scenarios = append(scenarios, execScenario{Command: "sh", Args: []string{"-c", row.script}, CWD: dir, Timeout: &timeout})
	}
	return scenarios
}

// pigExecOutcome runs scenario through ExecCommand and returns the outcome in the shape of the oracle's JSON.
func pigExecOutcome(ctx context.Context, scenario execScenario) execOutcome {
	options := &ExecOptions{MaxBuffer: scenario.MaxBuffer}
	if scenario.Timeout != nil {
		options.Timeout = *scenario.Timeout
	}
	result, err := ExecCommand(ctx, scenario.CWD, scenario.Command, scenario.Args, options)
	var got execOutcome
	if err != nil {
		got.Thrown = err.Error()
	} else {
		got = execOutcome{Stdout: result.Stdout, Stderr: result.Stderr, Code: result.Code, Killed: result.Killed, Truncated: result.Truncated}
	}
	raw, _ := json.Marshal(got)
	var normalized execOutcome
	_ = json.Unmarshal(raw, &normalized)
	return normalized
}

// execTruncatedRuns is how many times TestExecCommandMatchesPi runs each row whose output exceeds the cap, on each side, for the codes those runs report.
const execTruncatedRuns = 40

// codeCounts is how many truncated runs ended with each code.
type codeCounts map[int]int

func (c codeCounts) runs() int {
	total := 0
	for _, n := range c {
		total += n
	}
	return total
}

// mostly is the code of more than half of the runs, when there is one.
func (c codeCounts) mostly() (int, bool) {
	runs := c.runs()
	for code, n := range c {
		if 2*n > runs {
			return code, true
		}
	}
	return 0, false
}

func (c codeCounts) String() string {
	parts := make([]string, 0, len(c))
	for _, code := range slices.Sorted(maps.Keys(c)) {
		parts = append(parts, fmt.Sprintf("%d x%d", code, c[code]))
	}
	return strings.Join(parts, ", ")
}

// execSocketResetScript is the maxBuffer script whose codes in Pi are those of a socket pair, which PiG does not give a command:
// yes is the command itself and writes its stdout. Node's stdio are socket pairs, and the first write to a socket whose peer closed with unread data
// fails with ECONNRESET and no SIGPIPE, so yes goes on to report "yes: standard output: Connection reset by peer" on stderr: it exits 1 when it gets
// that far, the SIGTERM ends it while it reports (143), or the report meets the closed stderr (141). The write of a pipe raises SIGPIPE at once, so
// PiG's yes ends 141 unless the SIGTERM lands in the few microseconds before. Measured on xd6702 (96 CPUs, dash as sh), one row at a time: Pi 1 in
// 257 of 3600 runs (7%) inside one Node process of 34800 calls, and in 167 and 159 of 360 (46%, 44%) inside Node processes of 240 calls, where the
// JavaScript between Pi's close and its signal is not yet compiled; of 1200 runs with stderr in a file, Pi's 16 that ended 1 all held that report and
// 664 of its 920 that ended 143 held its beginning. PiG: 141 in 3571 of 3600 and 143 in 29, never 1. `exec yes >&2` and `yes; echo done` do not
// show it: there the write that meets the reset is followed by one that meets SIGPIPE, in the same process or in sh.
// pig divergence (D631): ExecCommand gives the command pipes, so the code Pi reports most for this script is not asked of PiG.
const execSocketResetScript = "exec yes"

// truncatedCodeFaults compares the codes PiG and Pi reported for the truncated runs of one script (its rows differ only in the cap, which the first
// chunk that crosses it ends), counted over the same number of runs in the same test run, and returns what differs. No set of codes is written here: Pi's
// runs name them.
//
// The code of a truncated run is what reaches the command first after exec.ts closed the pipes and sent SIGTERM (exec.ts:111-113): its own exit (1),
// the SIGPIPE of its next write (141) or the SIGTERM (143). No such row has one code in Pi: over 400 runs of each row, one at a time, a printf row,
// which ends by itself, reported 143 in 2 of 8400 runs of Pi (181 of 9408 with 64 at once) and, with the pipes closed first, in 4 of 8400 of PiG, and
// `yes; echo done` reported 141 in 66 of 3600 for Pi and 3 of 3600 for PiG. A sample therefore cannot hold every code a side can report, and a code
// one side reports once in thousands of runs is no difference; comparing the two samples code by code would fail on those. What is compared is the
// code a side reports for more than half of its runs:
//
//   - Pi's must be one PiG reports. Signalling before closing the pipes fails here: PiG then never reports the 141 of most of Pi's runs of
//     `exec yes >&2`. No share has to stay on one side of the half for this to hold: where the two sides give a command the same thing, each code
//     that can be Pi's most frequent was at least 14% of PiG's runs in every form measured (one at a time, 16 and 64 at once), so 360 runs hold it.
//     socketReset (execSocketResetScript) leaves this out: Pi's shares are the socket's there, 1 can be its most frequent code and PiG never
//     reports it, and the 143 the socket makes frequent in Pi was 29 of PiG's 3600 runs.
//   - PiG's must be one Pi reports, on every script.
//   - every code PiG reports must be one Pi reports for some truncated row of the same run (piAll).
func truncatedCodeFaults(pig, pi, piAll codeCounts, socketReset bool) []string {
	var faults []string
	if code, ok := pi.mostly(); ok && !socketReset && pig[code] == 0 {
		faults = append(faults, fmt.Sprintf("Pig never reports %d, the code of %d of Pi's %d runs", code, pi[code], pi.runs()))
	}
	if code, ok := pig.mostly(); ok && pi[code] == 0 {
		faults = append(faults, fmt.Sprintf("Pig reports %d in %d of %d runs, and Pi never does", code, pig[code], pig.runs()))
	}
	for _, code := range slices.Sorted(maps.Keys(pig)) {
		if piAll[code] == 0 {
			faults = append(faults, fmt.Sprintf("Pig reports %d, which Pi reports for no truncated row", code))
		}
	}
	return faults
}

// sampleTruncatedCodes runs every row of rows execTruncatedRuns times through Pi, in one Node process as the test's other rows, and then through PiG,
// and returns the codes of each side per script. Every run of either side must be truncated and killed.
func sampleTruncatedCodes(t *testing.T, rows []execScenario) (pi, pig map[string]codeCounts) {
	t.Helper()
	list := make([]execScenario, 0, len(rows)*execTruncatedRuns)
	for range execTruncatedRuns {
		list = append(list, rows...)
	}
	piOutcomes := piExecOutcomes(t, list)
	pigOutcomes := make([]execOutcome, len(list))
	for i, scenario := range list {
		pigOutcomes[i] = pigExecOutcome(context.Background(), scenario)
	}
	count := func(side string, outcomes []execOutcome) map[string]codeCounts {
		counts := map[string]codeCounts{}
		complete := map[string]int{}
		for i, outcome := range outcomes {
			script := list[i].Args[1]
			if !outcome.Truncated || !outcome.Killed {
				complete[script]++
				continue
			}
			if counts[script] == nil {
				counts[script] = codeCounts{}
			}
			counts[script][outcome.Code]++
		}
		for script, n := range complete {
			t.Errorf("%s: %d runs of %q over its cap were not truncated and killed", side, n, script)
		}
		return counts
	}
	return count("Pi", piOutcomes), count("Pig", pigOutcomes)
}

// execOracleScript is testdata/exec.mjs, which a test that changes its working
// directory still finds.
var execOracleScript, _ = filepath.Abs("testdata/exec.mjs")

// execOracleNode is node as the PATH the tests start with finds it, so a test
// that sets a slow PATH for the command under test does not search it for node.
// Without node on that PATH it stays "node" and the oracle fails to start.
var execOracleNode = func() string {
	if path, err := exec.LookPath("node"); err == nil {
		return path
	}
	return "node"
}()

// piExecOutcomes runs testdata/exec.mjs, Pi's execCommand, on scenarios in
// order and returns its outcomes.
func piExecOutcomes(t *testing.T, scenarios []execScenario) []execOutcome {
	t.Helper()
	expected, err := runPiExec(t.Context(), scenarios)
	if err != nil {
		t.Fatal(err)
	}
	return expected
}

func runPiExec(ctx context.Context, scenarios []execScenario) ([]execOutcome, error) {
	input, err := json.Marshal(scenarios)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, execOracleNode, execOracleScript, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Pi oracle: %w\n%s", err, &stderr)
	}
	var expected []execOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		return nil, err
	}
	return expected, nil
}

// Timeouts under 2 ms and above 2^31 run as a 1 ms timer in Node and race the child, and so do timeouts of a few hundred milliseconds on a loaded host, so they are not generated; ExecOptions.cwd is applied by the extension API in Pi, not by execCommand, so it is not compared here.
//
// execCommand (core/exec.ts) against pinned Pi over about 150 real commands: exit codes (including 256, which wraps, and a signal-killed child that resolves as 128 + the signal number), stdout and stderr (wide text, invalid UTF-8, 300KB, output after a backgrounded child), a timeout that fires after a sleep, one that is not reached, one that follows the first output and one that SIGTERM does not obey (execTimerScenarios), a timeout of 1e9 milliseconds, none, zero or negative, and the killed flag it sets, a pre-aborted signal, a child that ignores SIGTERM, a working directory that exists, is overridden or is missing, a program that cannot start (code 1), an empty or NUL-bearing command, argument or directory, and a directory as the command; the result or the error code and message agree.
func TestExecCommandMatchesPi(t *testing.T) {
	dir := t.TempDir()
	scenarios := execScenarios(dir)
	expected := piExecOutcomes(t, scenarios)
	failures := 0
	// The rows whose output both sides truncated: their codes are compared over execTruncatedRuns runs below, everything else here.
	var truncated []execScenario
	for i, scenario := range scenarios {
		ctx := context.Background()
		if scenario.Aborted {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		normalized := pigExecOutcome(ctx, scenario)
		raw, _ := json.Marshal(normalized)
		want := expected[i]
		if normalized.Truncated && want.Truncated {
			truncated = append(truncated, scenario)
			normalized.Code, want.Code = 1, 1
		}
		if normalized.Thrown != "" && want.Thrown != "" {
			// Pi's message starts with the Node error code; Pig reports Node's message text.
			if _, message, ok := strings.Cut(want.Thrown, ":"); ok && strings.Contains(normalized.Thrown, strings.TrimSpace(message)[:min(len(strings.TrimSpace(message)), 40)]) {
				continue
			}
		}
		if reflect.DeepEqual(normalized, want) {
			continue
		}
		// exec.ts kills a pre-aborted child right after spawn, so a fast command such as echo may or may not write before
		// SIGTERM lands, in Pi as in Pig. Each side's output must then be a prefix of what the command writes.
		if scenario.Aborted && normalized.Killed && want.Killed && normalized.Code == want.Code && normalized.Thrown == want.Thrown {
			full := strings.Join(scenario.Args, " ") + "\n"
			if scenario.Command == "echo" && strings.HasPrefix(full, normalized.Stdout) && strings.HasPrefix(full, want.Stdout) && normalized.Stderr == want.Stderr {
				continue
			}
		}
		// A timer that ends Git for Windows' sh while it is starting `exec sleep 60` leaves the starting process with a parent that is gone, and
		// the MSYS runtime prints its child_copy failure on the inherited stderr. exec.ts appends whatever reaches the pipes until they end or
		// fall idle (core/exec.ts:81-87 has no kill check, utils/child-process.ts:93-97 re-arms the grace on every chunk), in Pi as in Pig, so
		// whether the line lands inside the grace is the race between a loaded host and a 100 ms timer on either side.
		if runtime.GOOS == "windows" && sameButForCygwinKillNoise(scenario, normalized, want) {
			continue
		}
		if failures++; failures > 8 {
			continue
		}
		in, _ := json.Marshal(scenario)
		e, _ := json.Marshal(want)
		t.Errorf("scenario %d %.300s\n  Pig %.400s\n  Pi  %.400s", i, in, raw, e)
	}
	if failures > 8 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
	pi, pig := sampleTruncatedCodes(t, truncated)
	piAll := codeCounts{}
	for _, counts := range pi {
		for code, n := range counts {
			piAll[code] += n
		}
	}
	for _, script := range slices.Sorted(maps.Keys(pi)) {
		t.Logf("truncated runs of %q: Pig %s | Pi %s", script, pig[script], pi[script])
		for _, fault := range truncatedCodeFaults(pig[script], pi[script], piAll, script == execSocketResetScript) {
			t.Errorf("truncated runs of %q: %s\n  Pig %s\n  Pi  %s", script, fault, pig[script], pi[script])
		}
	}
}
