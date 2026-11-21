package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type execScenario struct {
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	CWD       string   `json:"cwd"`
	Timeout   *float64 `json:"timeout,omitempty"`
	MaxBuffer *float64 `json:"maxBuffer,omitempty"`
	Aborted   bool     `json:"aborted,omitempty"`
	// AbortWhenFile aborts the call's signal once the command has created that file (testdata/exec.mjs removes it first), so the abort comes while the
	// command runs.
	AbortWhenFile string `json:"abortWhenFile,omitempty"`
	// ExactCode marks a truncated row whose code is not a race, so that TestExecCommandMatchesPi compares it like the code of any other row.
	ExactCode bool `json:"-"`
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
	for _, script := range []string{"printf 'a€🙂'", "printf 'a€🙂' >&2", "printf 'ab' ; printf 'cd' >&2", "printf '\\342'; printf '\\360' >&2", "printf '\\342\\202'", execSocketResetScript, "exec yes >&2", "yes; echo done", "printf x"} {
		limits := []float64{0, 1, 2, 3, 4, 5, 8, 9, 1000, 536870888}
		if strings.Contains(script, "yes") {
			// The cap alone ends an endless writer.
			limits = limits[:len(limits)-1]
		}
		for _, limit := range limits {
			l := limit
			args := sh(script)
			if script == execSocketResetScript {
				// $1 is the file that takes yes's stderr; the rows of one call share it, and the sampled runs have a file each.
				args = append(args, "sh", filepath.ToSlash(filepath.Join(dir, "said")))
			}
			scenarios = append(scenarios, execScenario{Command: "sh", Args: args, CWD: dir, MaxBuffer: &l})
		}
	}
	if runtime.GOOS != "windows" {
		scenarios = append(scenarios, execOrderScenarios(dir)...)
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

// execOrderScenarios are the rows where the order of what exec.ts does with the command's pipes and its signal shows in the result without a race:
// the command's handler for the SIGTERM runs after the call has closed the pipes (a cap: exec.ts:111-113) or while they are open (an abort:
// exec.ts:88-94, which signals and nothing else). Nothing in them depends on how soon the handler runs, which `while :; do sleep 0.05; done` leaves to
// the end of a sleep. They need signals, so they are not run on Windows, where the kill ends the shell.
func execOrderScenarios(dir string) []execScenario {
	sh := func(script string, args ...string) []string { return append([]string{"-c", script}, args...) }
	three, ten := 3.0, 10.0
	ready := filepath.ToSlash(filepath.Join(dir, "abort-when-ready"))
	return []execScenario{
		// The handler writes to pipes that are closed: the shell dies of the SIGPIPE of its echo (141), in Pi and in PiG.
		{Command: "sh", Args: sh("trap 'echo caught; exit 3' TERM; printf 12345; while :; do sleep 0.05; done"), CWD: dir, MaxBuffer: &three, ExactCode: true},
		// A handler that writes nothing ends the shell with its own status.
		{Command: "sh", Args: sh("trap 'exit 3' TERM; printf 12345; while :; do sleep 0.05; done"), CWD: dir, MaxBuffer: &three, ExactCode: true},
		// A descendant that keeps writing meets the closed pipe and dies of SIGPIPE; the shell, waiting for it, ends by the SIGTERM (143).
		{Command: "sh", Args: sh("(while :; do echo tick; sleep 0.01; done) & wait"), CWD: dir, MaxBuffer: &ten, ExactCode: true},
		// An abort while the command runs: the pipes are open, so what the handler writes reaches the caller.
		{Command: "sh", Args: sh("trap 'echo bye; exit 4' TERM; echo ready; : > \"$1\"; while :; do sleep 0.05; done", "sh", ready), CWD: dir, AbortWhenFile: ready},
		// stdin is /dev/null ("ignore", exec.ts:61): a command that reads it until the end gets the end at once.
		{Command: "sh", Args: sh("read line; echo \"got:[$line] rc:$?\""), CWD: dir},
	}
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
	rows := []struct {
		timeout float64
		script  string
	}{
		{1000, "exec sleep 60"},
		{60000, "echo quick"},
		{3000, "echo before; exec sleep 60"},
		{1000, trapped},
	}
	if runtime.GOOS != "windows" {
		// The timer's SIGTERM reaches a command whose pipes are open (exec.ts:96-98 signals and nothing else), so what its handler writes reaches the
		// caller. The shell is in a loop of short sleeps, so its handler runs soon after the signal, whenever the call wakes.
		rows = append(rows, struct {
			timeout float64
			script  string
		}{3000, "trap 'echo caught; exit 3' TERM; echo ready; while :; do sleep 0.05; done"})
	}
	var scenarios []execScenario
	for _, row := range rows {
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
	if scenario.AbortWhenFile != "" {
		// The same abort as testdata/exec.mjs: once the command has created its file.
		signal, abort := context.WithCancel(ctx)
		defer abort()
		options.Signal = signal
		_ = os.Remove(scenario.AbortWhenFile)
		go func() {
			for ; signal.Err() == nil; time.Sleep(5 * time.Millisecond) {
				if _, err := os.Stat(scenario.AbortWhenFile); err == nil {
					abort()
				}
			}
		}()
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

func (c codeCounts) String() string {
	parts := make([]string, 0, len(c))
	for _, code := range slices.Sorted(maps.Keys(c)) {
		parts = append(parts, fmt.Sprintf("%d x%d", code, c[code]))
	}
	return strings.Join(parts, ", ")
}

// execSocketResetScript is the maxBuffer script whose runs are sorted by what yes said on stderr, because Pi's socket pairs give some of them an end that
// PiG's pipes cannot give. yes is the command itself and writes its stdout. The first write to a socket whose peer closed with unread data fails with
// ECONNRESET and no SIGPIPE, so Pi's yes goes on to report "yes: standard output: Connection reset by peer" on stderr: it exits 1 when it gets that far,
// the SIGTERM ends it while it reports (143), or the report meets the closed stderr (141). The write of a pipe raises SIGPIPE at once, so PiG's yes ends
// 141 unless the SIGTERM lands in the few microseconds before. Each run's stderr goes to a file of its own ($1), which the closed pipes of a truncated
// call do not hide, and cutOffEndOf (exec_cutoff_ends_test.go) tells the runs that reported a failed write from the rest.
//
// pig divergence (D631): the runs of Pi that reported a failed write are left out of Pi's codes, and only those: PiG's command writes a pipe and has no
// such end. A run of PiG that reports one is a fault. Every other run counts on both sides, under every rule of truncatedCodeFaults, so the code of the
// other ends is compared with Pi's like the code of any other row: 141 where the write meets the closed pipe before the SIGTERM, 143 where the SIGTERM
// comes first. Which of the two a side gives is a race between the command's write and the signal, and it moves with the host's load in Pi itself
// (see execTruncatedRounds, which takes both sides' runs alike under it).
const execSocketResetScript = `exec yes 2>"$1"`

// validTruncatedCode reports whether code is in the contract-defined valid set for a truncated run of script.
// exec.ts closes both pipes and sends SIGTERM (exec.ts:111-113) and rewrites exit 0 to 1 (exec.ts:154), so a truncated
// command that otherwise exits 0 can end with 1 (exited before the signal), 141 (SIGPIPE on a write after close) or
// 143 (SIGTERM). For execSocketResetScript, runs that reported a failed write on stderr are filtered out by cutOffEndOf
// (D631), leaving only 141 and 143.
func validTruncatedCode(script string, code int) bool {
	if script == execSocketResetScript {
		return code == 141 || code == 143
	}
	return code == 1 || code == 141 || code == 143
}

// truncatedCodeFaults checks that every exit code reported by PiG and Pi for the truncated runs of script belongs to
// the contract-defined valid set, without asserting any rate or majority of the race between exit, SIGPIPE and SIGTERM.
// The deterministic close-before-signal order is pinned separately by execOrderScenarios (ExactCode: true) and
// TestExecCommandClosesBothPipesBeforeItSignalsAtTheCap.
func truncatedCodeFaults(script string, pig, pi codeCounts) []string {
	var faults []string
	if pig.runs() == 0 {
		faults = append(faults, "Pig produced no truncated runs")
	}
	for _, code := range slices.Sorted(maps.Keys(pi)) {
		if !validTruncatedCode(script, code) {
			faults = append(faults, fmt.Sprintf("Pi reports %d, outside the valid truncated code set", code))
		}
	}
	for _, code := range slices.Sorted(maps.Keys(pig)) {
		if !validTruncatedCode(script, code) {
			faults = append(faults, fmt.Sprintf("Pig reports %d, outside the valid truncated code set", code))
		}
	}
	return faults
}

// execTruncatedRounds is how many rounds the execTruncatedRuns runs of a row are taken in. A round runs its share of every row through Pi, in one Node
// process as the test's other rows, and then through PiG. A run's code is a race between the command's write and the signal, which the host's load moves
// in Pi itself: `exec yes` alone ended 141 in 92% of the runs of Pi that reported nothing, and 143 in 97% of them beside busy loops on its CPUs. Taken
// one side after the other, a change of the load between the two would move one side's sample and not the other's; taken in rounds, it moves both alike.
const execTruncatedRounds = 4

// sampleTruncatedCodes runs every row of rows execTruncatedRuns times through each side, in execTruncatedRounds rounds, and returns the codes of each
// side per script and how many of Pi's runs of each script were left out (execSocketResetScript). Every run of either side must be truncated and killed.
func sampleTruncatedCodes(t *testing.T, dir string, rows []execScenario) (pi, pig map[string]codeCounts, left map[string]int) {
	t.Helper()
	pi, pig, left = map[string]codeCounts{}, map[string]codeCounts{}, map[string]int{}
	type sideScript struct{ side, script string }
	complete := map[sideScript]int{}
	for round := range execTruncatedRounds {
		list := make([]execScenario, 0, len(rows)*execTruncatedRuns/execTruncatedRounds)
		for range execTruncatedRuns / execTruncatedRounds {
			list = append(list, rows...)
		}
		// runsOf is list with a file of its own for the stderr of each run of execSocketResetScript, which is $1 of its script.
		runsOf := func(side string) []execScenario {
			runs := slices.Clone(list)
			for i := range runs {
				if runs[i].Args[1] == execSocketResetScript {
					runs[i].Args = slices.Clone(runs[i].Args)
					runs[i].Args[3] = filepath.ToSlash(filepath.Join(dir, fmt.Sprintf("said-%s-%d-%d", side, round, i)))
				}
			}
			return runs
		}
		count := func(side string, runs []execScenario, outcomes []execOutcome, counts map[string]codeCounts) {
			for i, outcome := range outcomes {
				script := runs[i].Args[1]
				if !outcome.Truncated || !outcome.Killed || outcome.Thrown != "" {
					complete[sideScript{side, script}]++
					continue
				}
				if runs[i].MaxBuffer != nil && int64(len(outcome.Stdout)+len(outcome.Stderr)) > int64(*runs[i].MaxBuffer) {
					t.Errorf("%s run of %q exceeded maxBuffer %.0f: got %d bytes", side, script, *runs[i].MaxBuffer, len(outcome.Stdout)+len(outcome.Stderr))
				}
				if script == execSocketResetScript {
					said, err := os.ReadFile(runs[i].Args[3])
					if err != nil {
						t.Fatal(err)
					}
					if end := cutOffEndOf(outcome, string(said)); end != nil && end.socket {
						if side != "Pi" {
							t.Errorf("%s run of %q = %+v reported a failed write (%q on stderr), which only the writer of a socket meets", side, script, outcome, said)
						} else {
							left[script]++
						}
						continue
					}
				}
				if counts[script] == nil {
					counts[script] = codeCounts{}
				}
				counts[script][outcome.Code]++
			}
		}
		piRuns, pigRuns := runsOf("Pi"), runsOf("Pig")
		count("Pi", piRuns, piExecOutcomes(t, piRuns), pi)
		pigOutcomes := make([]execOutcome, len(pigRuns))
		for i, scenario := range pigRuns {
			pigOutcomes[i] = pigExecOutcome(context.Background(), scenario)
		}
		count("Pig", pigRuns, pigOutcomes, pig)
	}
	for key, n := range complete {
		t.Errorf("%s: %d runs of %q over its cap were not truncated and killed", key.side, n, key.script)
	}
	return pi, pig, left
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
	// Clear files created in dir during the Pi batch (e.g. "said" from execSocketResetScript)
	// so PiG runs in the same empty working directory (on Windows MSYS echo expands "*" to leftover files).
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	failures := 0
	// The rows whose output both sides truncated: their codes are compared over execTruncatedRuns runs below, everything else here.
	var truncated []execScenario
	// The pre-aborted echo races Pi's own SIGTERM, so it is sampled below, and every other row here is an exact comparison.
	var racedAborts []execScenario
	for i, scenario := range scenarios {
		if scenario.Aborted && scenario.Command == "echo" {
			racedAborts = append(racedAborts, scenario)
			continue
		}
		ctx := context.Background()
		if scenario.Aborted {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		normalized := pigExecOutcome(ctx, scenario)
		raw, _ := json.Marshal(normalized)
		want := expected[i]
		if normalized.Truncated && want.Truncated && !scenario.ExactCode {
			truncated = append(truncated, scenario)
			normalized.Code, want.Code = 1, 1
		}
		if normalized.Thrown != "" && want.Thrown != "" {
			// Pi's message starts with the Node error code; Pig reports Node's message text.
			if _, message, ok := strings.Cut(want.Thrown, ":"); ok && strings.Contains(normalized.Thrown, strings.TrimSpace(message)[:min(len(strings.TrimSpace(message)), 40)]) {
				continue
			}
		}
		if scenario.Aborted && (!normalized.Killed || normalized.Stdout != "" || normalized.Stderr != "" || normalized.Code != 143 ||
			!want.Killed || want.Stdout != "" || want.Stderr != "" || want.Code != 143) {
			t.Errorf("scenario %d (%s %v): pre-aborted deterministic command must be killed without output (code 143)\n  Pig %+v\n  Pi  %+v", i, scenario.Command, scenario.Args, normalized, want)
			continue
		}
		if reflect.DeepEqual(normalized, want) {
			continue
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
	for _, scenario := range racedAborts {
		checkAbortedBeforeStart(t, scenario)
	}
	pi, pig, left := sampleTruncatedCodes(t, dir, truncated)
	scripts := map[string]bool{}
	for _, row := range truncated {
		scripts[row.Args[1]] = true
	}
	for _, script := range slices.Sorted(maps.Keys(scripts)) {
		t.Logf("truncated runs of %q: Pig %s | Pi %s (left out of Pi's: %d runs that reported a failed write)", script, pig[script], pi[script], left[script])
		for _, fault := range truncatedCodeFaults(script, pig[script], pi[script]) {
			t.Errorf("truncated runs of %q: %s\n  Pig %s\n  Pi  %s", script, fault, pig[script], pi[script])
		}
	}
}

// execAbortedSamples is how many times a pre-aborted echo is run through each side: 400 runs of Pi in its Node process take about two seconds.
const execAbortedSamples = 400

// checkAbortedBeforeStart checks the scenario of a command whose signal was already aborted before the call. Both core/exec.ts (lines 58-62, 88-90)
// and PiG (coding/extension/exec.go) spawn the child and immediately send SIGTERM, so a fast command like echo races the signal: it may be killed
// before writing (empty stdout, code 143), killed after writing a prefix or all of its output (code 143), or finish writing and exit 0 before the
// signal lands (full stdout, code 0). On both sides, Killed is always true, Thrown and Stderr are empty, and Truncated is false. Which variant wins
// more often depends on host scheduling and CPU count, so no rate or majority of the race is asserted; every run on both sides must belong to that
// contract-defined valid set. The deterministic killed-without-output check is the pre-aborted `sleep 3` scenario in TestExecCommandMatchesPi.
func checkAbortedBeforeStart(t *testing.T, scenario execScenario) {
	t.Helper()
	pi := map[execOutcome]int{}
	for _, outcome := range piExecOutcomes(t, slices.Repeat([]execScenario{scenario}, execAbortedSamples)) {
		pi[outcome]++
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pig := map[execOutcome]int{}
	for range execAbortedSamples {
		pig[pigExecOutcome(ctx, scenario)]++
	}
	t.Logf("aborted before start %q: Pig %v | Pi %v", scenario.Args, pig, pi)
	full := strings.Join(scenario.Args, " ") + "\n"
	valid := func(outcome execOutcome) bool {
		return outcome.Killed && outcome.Thrown == "" && outcome.Stderr == "" && !outcome.Truncated &&
			strings.HasPrefix(full, outcome.Stdout) &&
			(outcome.Code == 143 || outcome.Code == 0 && outcome.Stdout == full)
	}
	for outcome := range pi {
		if !valid(outcome) {
			t.Errorf("Pi ended a run of %q as %+v, which no race between the child's exit and the SIGTERM gives; Pi %v", scenario.Args, outcome, pi)
		}
	}
	for outcome := range pig {
		if !valid(outcome) {
			t.Errorf("Pig ended a run of %q as %+v, which no race between the child's exit and the SIGTERM gives; Pig %v", scenario.Args, outcome, pig)
		}
	}
}
