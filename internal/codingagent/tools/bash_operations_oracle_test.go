package tools

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type bashOpsProbe struct {
	Command      string            `json:"command"`
	Cwd          string            `json:"cwd"`
	Timeout      *float64          `json:"timeout"`
	AbortAfterMs int               `json:"abortAfterMs"`
	PreAborted   bool              `json:"preAborted"`
	UseSignal    bool              `json:"useSignal"`
	Env          map[string]string `json:"env"`
}

type bashOpsOutcome struct {
	ExitCode *int   `json:"exitCode"`
	Thrown   string `json:"thrown"`
	Output   string `json:"output"`
}

// createLocalBashOperations().exec (core/tools/bash.ts) against pinned Pi over real bash commands: exit codes, death by signal (128 + n), output in UTF-8 and invalid bytes, large output, a missing or non-directory working directory, timeouts (valid, zero, negative, too large) and aborts before and during a run, a background process that outlives the shell, and an explicit environment. Compared: the exit code or the error, and the bytes delivered to onData. Stdout and stderr are never mixed in one command, since their interleaving is not deterministic.
func TestLocalBashOperationsMatchPi(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	commands := []string{
		"echo hello", "printf 'a\\nb'", "exit 3", "exit 0", "false", "true", "pwd", "ls", "cat file.txt", "cat missing.txt", "echo $FOO", "echo \"$0\"",
		"kill -9 $$", "kill -TERM $$", "kill -INT $$", "sleep 5", "echo start; sleep 5; echo end", "(sleep 0.4; echo bg) & echo fg", "printf '%s\\n' $(seq 1 4000)",
		"echo -n é日本😀", "printf '\\xff\\xfe\\xc3'", "cd /nonexistent; echo after", "exit 256", "exit 255", "echo out; exit 7", "trap 'echo trapped' TERM; sleep 5",
		"echo 1>&2 err-only", "printf 'no newline'", "sleep 0.1; echo late", "yes | head -n 2000", "exec true", "exit", "echo $((1+1))", "set -e; false; echo no",
		"sleep 0.3 & wait", "echo a; echo b", "printf '\\033[31mred\\033[0m'", "printf 'x%.0s' $(seq 1 70000)",
	}
	timeouts := []*float64{nil, nil, nil, nil, nil, nil, nil, new(30.0), new(30.0), new(0.0), new(-1.0), new(1e10), new(2147483.647), new(2147483.648)}
	rng := rand.New(rand.NewPCG(13, 29))
	var probes []bashOpsProbe
	for _, command := range commands {
		for range 3 {
			probe := bashOpsProbe{Command: command, Cwd: dir, UseSignal: rng.IntN(3) != 0, Timeout: timeouts[rng.IntN(len(timeouts))]}
			switch rng.IntN(12) {
			case 0:
				probe.Cwd = missing
			case 1:
				probe.Cwd = filepath.Join(dir, "file.txt")
			case 2:
				probe.PreAborted = true
			case 3, 4:
				// Only a command that is still running at the abort has a deterministic outcome.
				if strings.Contains(command, "sleep 5") {
					probe.AbortAfterMs = 60 + rng.IntN(120)
				}
			case 5:
				probe.Env = map[string]string{"FOO": "bar", "PATH": "/usr/bin:/bin"}
			case 6:
				probe.Cwd = locked
			}
			// A long sleep with no abort and no short timeout would run its full five seconds on both sides.
			if strings.Contains(command, "sleep 5") && probe.AbortAfterMs == 0 && !probe.PreAborted && (probe.Timeout == nil || (*probe.Timeout >= 30 && *probe.Timeout < 1e9)) {
				probe.Timeout = new(0.4)
			}
			probes = append(probes, probe)
		}
	}
	// Commands that outlive a short timeout or an abort, with and without output before the kill, a trap, and a background child.
	for _, command := range []string{"sleep 5", "echo start; sleep 5; echo end", "trap 'echo trapped' TERM; sleep 5", "(sleep 5) & sleep 5", "sleep 5 & wait"} {
		for _, timeout := range []float64{0.4, 0.9} {
			probes = append(probes, bashOpsProbe{Command: command, Cwd: dir, UseSignal: true, Timeout: new(timeout)})
		}
		probes = append(probes, bashOpsProbe{Command: command, Cwd: dir, UseSignal: true, AbortAfterMs: 300}, bashOpsProbe{Command: command, Cwd: dir, UseSignal: true, AbortAfterMs: 300, Timeout: new(30.0)}, bashOpsProbe{Command: command, Cwd: dir, UseSignal: false, AbortAfterMs: 300, Timeout: new(0.5)})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/bash_operations.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []bashOpsOutcome
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	operations := CreateLocalBashOperations(nil)
	mismatches, thrown, signalled := 0, 0, 0
	for i, probe := range probes {
		ctx, cancel := context.WithCancel(t.Context())
		runCtx := ctx
		if !probe.UseSignal {
			runCtx = context.WithoutCancel(ctx)
		}
		if probe.PreAborted {
			cancel()
		}
		var timer *time.Timer
		if probe.AbortAfterMs > 0 {
			timer = time.AfterFunc(time.Duration(probe.AbortAfterMs)*time.Millisecond, cancel)
		}
		var mu sync.Mutex
		var collected bytes.Buffer
		options := BashOperationsExecOptions{OnData: func(data []byte) {
			mu.Lock()
			collected.Write(data)
			mu.Unlock()
		}, Timeout: probe.Timeout}
		if probe.Env != nil {
			for key, value := range probe.Env {
				options.Env = append(options.Env, key+"="+value)
			}
		}
		result, runErr := operations.Exec(runCtx, probe.Command, probe.Cwd, options)
		if timer != nil {
			timer.Stop()
		}
		cancel()
		mu.Lock()
		got := bashOpsOutcome{Output: hex.EncodeToString(collected.Bytes())}
		mu.Unlock()
		if runErr != nil {
			got.Thrown = runErr.Error()
		} else {
			got.ExitCode = result.ExitCode
		}
		w := want[i]
		if w.Thrown != "" {
			thrown++
		}
		if w.ExitCode != nil && *w.ExitCode > 128 {
			signalled++
		}
		if !sameBashOpsOutcome(got, w, probe) {
			if mismatches++; mismatches <= 8 {
				t.Errorf("probe %d %s: Go %s, Pi %s", i, describeBashOpsProbe(probe), describeBashOpsOutcome(got), describeBashOpsOutcome(w))
			}
		}
	}
	if mismatches > 8 {
		t.Errorf("%d of %d probes differ from Pi", mismatches, len(probes))
	}
	kinds := map[string]int{}
	for _, w := range want {
		kinds[strings.SplitN(strings.SplitN(w.Thrown, ":", 2)[0], "\n", 2)[0]]++
	}
	t.Logf("Pi outcomes: %v", kinds)
	if thrown < 40 || signalled < 5 {
		t.Errorf("thin coverage: %d errors, %d signal exits", thrown, signalled)
	}
}

func sameBashOpsOutcome(got, want bashOpsOutcome, probe bashOpsProbe) bool {
	if got.Thrown != want.Thrown || (got.ExitCode == nil) != (want.ExitCode == nil) || (got.ExitCode != nil && *got.ExitCode != *want.ExitCode) {
		return false
	}
	// A run cut short by an abort or a timeout delivers whatever arrived before the kill, which depends on timing.
	if want.Thrown == "aborted" || strings.HasPrefix(want.Thrown, "timeout:") {
		return true
	}
	if got.Output == want.Output {
		return true
	}
	// stdout and stderr reach the collector through two pipes, so when a command writes to both, which lands first is
	// the scheduler's choice in Pi and in Go alike ("cd /nonexistent; echo after": the error and "after" swap). The
	// same lines in another order are the same outcome.
	return sameLinesAnyOrder(got.Output, want.Output)
}

// sameLinesAnyOrder reports whether two hex-encoded outputs hold the same lines, each the same number of times.
func sameLinesAnyOrder(gotHex, wantHex string) bool {
	got, err1 := hex.DecodeString(gotHex)
	want, err2 := hex.DecodeString(wantHex)
	if err1 != nil || err2 != nil || len(got) != len(want) {
		return false
	}
	a := strings.SplitAfter(string(got), "\n")
	b := strings.SplitAfter(string(want), "\n")
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func describeBashOpsProbe(probe bashOpsProbe) string {
	timeout := "none"
	if probe.Timeout != nil {
		timeout = fmt.Sprint(*probe.Timeout)
	}
	return fmt.Sprintf("%q cwd-base %q timeout %s abortAfter %d pre %v signal %v env %v", probe.Command, filepath.Base(probe.Cwd), timeout, probe.AbortAfterMs, probe.PreAborted, probe.UseSignal, probe.Env != nil)
}

func describeBashOpsOutcome(outcome bashOpsOutcome) string {
	code := "nil"
	if outcome.ExitCode != nil {
		code = fmt.Sprint(*outcome.ExitCode)
	}
	output := outcome.Output
	if len(output) > 60 {
		output = output[:60] + "…"
	}
	return fmt.Sprintf("exit %s thrown %q output %s", code, outcome.Thrown, output)
}
