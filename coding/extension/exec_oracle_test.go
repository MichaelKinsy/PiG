package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type execScenario struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
	Timeout *float64 `json:"timeout,omitempty"`
	Aborted bool     `json:"aborted,omitempty"`
}

type execOutcome struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Code   int    `json:"code"`
	Killed bool   `json:"killed"`
	Thrown string `json:"thrown,omitempty"`
}

func execScenarios(dir string) []execScenario {
	rng := rand.New(rand.NewPCG(31, 7))
	sh := func(script string) []string { return []string{"-c", script} }
	scripts := []string{"echo out; echo err >&2; exit 3", "exit 0", "exit 255", "exit 256", "echo -n no-newline", "echo 日本語 ✓; echo 😀 >&2", "printf '\\377\\376abc'", "kill -9 $$", "kill -TERM $$", "kill -HUP $$", "echo $0", "pwd", "head -c 300000 /dev/zero | tr '\\0' x", "echo start; sleep 1 & echo background", "cat; echo after-cat", "printf 'a\\nb\\n'; printf 'c' >&2", "ls /nonexistent-path-for-oracle", "true", "false", "case $$ in *[!0-9]*) echo not-numeric;; *) echo numeric-pid;; esac"}
	var scenarios []execScenario
	for _, script := range scripts {
		scenarios = append(scenarios, execScenario{Command: "sh", Args: sh(script), CWD: dir})
	}
	// A timer that fires leaves each script a second on either side: on a loaded host sh can take hundreds of milliseconds to
	// start and run its first command (bash started as "sh" also searches the PATH for itself), in Pi as in PiG, so a shorter
	// timeout races the child and the two runs can end differently.
	for _, timeout := range []float64{1000, 0, -5, 1e9} {
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
	exits := []int{0, 1, 2, 42, 127, 128, 130, 255}
	for range 60 {
		script := "echo " + strings.Repeat("x", rng.IntN(50)) + "; echo e >&2; exit " + []string{"0", "1", "2", "42", "127", "130", "255"}[rng.IntN(len(exits)-1)]
		scenarios = append(scenarios, execScenario{Command: "sh", Args: sh(script), CWD: dir})
	}
	return scenarios
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
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), execOracleNode, execOracleScript, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []execOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	return expected
}

// Timeouts under 2 ms and above 2^31 run as a 1 ms timer in Node and race the child, and so do timeouts of a few hundred milliseconds on a loaded host, so they are not generated; ExecOptions.cwd is applied by the extension API in Pi, not by execCommand, so it is not compared here.
//
// execCommand (core/exec.ts) against pinned Pi over about 150 real commands: exit codes (including 256, which wraps, and a signal-killed child that resolves as 0), stdout and stderr (wide text, invalid UTF-8, 300KB, output after a backgrounded child), a timeout of 1000 or 1e9 milliseconds, none, zero or negative, and the killed flag it sets, a pre-aborted signal, a child that ignores SIGTERM, a working directory that exists, is overridden or is missing, a program that cannot start (code 1), an empty or NUL-bearing command, argument or directory, and a directory as the command; the result or the error code and message agree.
func TestExecCommandMatchesPi(t *testing.T) {
	dir := t.TempDir()
	scenarios := execScenarios(dir)
	expected := piExecOutcomes(t, scenarios)
	failures := 0
	for i, scenario := range scenarios {
		ctx := context.Background()
		if scenario.Aborted {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		options := &ExecOptions{}
		if scenario.Timeout != nil {
			options.Timeout = *scenario.Timeout
		}
		result, err := ExecCommand(ctx, scenario.CWD, scenario.Command, scenario.Args, options)
		var got execOutcome
		if err != nil {
			got.Thrown = err.Error()
		} else {
			got = execOutcome{Stdout: result.Stdout, Stderr: result.Stderr, Code: result.Code, Killed: result.Killed}
		}
		raw, _ := json.Marshal(got)
		var normalized execOutcome
		_ = json.Unmarshal(raw, &normalized)
		want := expected[i]
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
}
