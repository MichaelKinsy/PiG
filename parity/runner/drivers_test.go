//go:build parity

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pi's main.ts exits immediately after listModels logs its table. Node writes
// asynchronously to pipes on POSIX, so the CLI comparator must not lose queued
// rows when the table grows beyond pipe capacity.
func TestCLIModeDriverCapturesImmediateNodeExit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"pig", "pi"} {
		for _, tc := range []struct {
			name  string
			rows  int
			input []string
		}{
			{name: "empty"},
			{name: "ordinary", rows: 3},
			{name: "large", rows: 1 << 19},
			{name: "large-with-input", rows: 1 << 19, input: []string{"input"}},
		} {
			t.Run(label+"/"+tc.name, func(t *testing.T) {
				cwd := t.TempDir()
				script := fmt.Sprintf(`
const fs = require("node:fs");
if (%t) process.stdout.write(fs.readFileSync(0, "utf8"));
process.stdout.write("model-row\n".repeat(%d));
process.stderr.write("stderr-tail\n");
process.exit(7);
`, len(tc.input) > 0, tc.rows)
				scenario := &Scenario{
					Name: "capture-immediate-exit", SourcePath: filepath.Join(cwd, "scenario.toml"),
					CLI: CLIDriverConfig{CWD: cwd, Args: []string{"-e", script}, InputLines: tc.input, TimeoutSeconds: 5},
				}
				result := (cliModeDriver{}).Run(t.Context(), t, BinaryRef{Label: label, Path: node}, scenario)
				if result.Err != nil || result.ExitCode != 7 {
					t.Fatalf("exit code=%d err=%v, want exit 7", result.ExitCode, result.Err)
				}
				want := strings.Repeat("model-row\n", tc.rows) + "stderr-tail\n"
				if len(tc.input) > 0 {
					want = "input\n" + want
				}
				if result.Output != want {
					t.Fatalf("captured %d bytes, want all %d bytes in stdout/stderr order before immediate exit", len(result.Output), len(want))
				}
			})
		}
	}
}

func TestCLIModeDriverRemovesCaptureAfterSuccessAndFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name, command, output string
		code                  int
	}{
		{name: "success", command: "printf stdout; printf stderr >&2", output: "stdoutstderr"},
		{name: "failure", command: "printf partial; exit 7", output: "partial", code: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := &Scenario{
				Name: "capture-cleanup", SourcePath: filepath.Join(root, "scenario.toml"),
				CLI: CLIDriverConfig{CWD: root, Args: []string{"-c", tc.command}},
			}
			result := (cliModeDriver{}).Run(t.Context(), t, BinaryRef{Label: "pig", Path: "/bin/sh"}, scenario)
			if result.Err != nil || result.ExitCode != tc.code || result.Output != tc.output {
				t.Fatalf("result = %+v, want code %d and output %q", result, tc.code, tc.output)
			}
		})
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("capture files survived %s cleanup: %v", tc.name, entries)
		}
	}
}

func commandOutputFile(t *testing.T, file bool) *os.File {
	t.Helper()
	if !file {
		return nil
	}
	output, err := os.CreateTemp(t.TempDir(), "output-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	return output
}

func TestRunCmdWithInputPropagatesBrokenPipe(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(fmt.Sprintf("file=%t", file), func(t *testing.T) {
			_, _, _, err := runCmdWithInput(
				context.Background(),
				"/bin/sh",
				[]string{"-c", "IFS= read -r first; exit 0"},
				nil,
				[]string{"first", strings.Repeat("x", 4<<20)},
				0,
				5*time.Second,
				"",
				commandOutputFile(t, file),
			)
			if err == nil || !strings.Contains(err.Error(), "write stdin") {
				t.Fatalf("input delivery error = %v, want broken-pipe write error", err)
			}
		})
	}
}

func TestRunCmdWithInputCancellationInterruptsSettle(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(fmt.Sprintf("file=%t", file), func(t *testing.T) {
			start := time.Now()
			_, _, _, err := runCmdWithInput(
				context.Background(),
				"/bin/sh",
				[]string{"-c", "cat >/dev/null"},
				nil,
				[]string{"input"},
				10*time.Second,
				50*time.Millisecond,
				"",
				commandOutputFile(t, file),
			)
			if err == nil || !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("settle cancellation error = %v", err)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("settle cancellation took %s", elapsed)
			}
		})
	}
}

type countingDriver struct{ calls int }

func (*countingDriver) Name() string { return "counting" }
func (d *countingDriver) Run(context.Context, *testing.T, BinaryRef, *Scenario) Result {
	d.calls++
	return Result{ReadyOK: true, Err: context.DeadlineExceeded}
}

type recordingOrderDriver struct{ labels []string }

func (*recordingOrderDriver) Name() string { return "recording-order" }
func (d *recordingOrderDriver) Run(_ context.Context, _ *testing.T, bin BinaryRef, _ *Scenario) Result {
	d.labels = append(d.labels, bin.Label)
	return Result{ReadyOK: true, RuntimeMs: 1}
}

func TestRunScenarioAlternatesSerialPairOrder(t *testing.T) {
	driver := &recordingOrderDriver{}
	const name = "recording-order"
	DriverRegistry[name] = driver
	defer delete(DriverRegistry, name)
	scenario := &Scenario{Driver: name, Assert: AssertSpec{Runs: 4, RuntimeRatioMax: 2}}
	outcome := RunScenario(context.Background(), t, scenario, BinaryRef{Label: "pig"}, BinaryRef{Label: "pi"}, false)
	if got, want := strings.Join(driver.labels, ","), "pig,pi,pi,pig,pig,pi,pi,pig"; got != want {
		t.Fatalf("serial launch order = %s, want %s", got, want)
	}
	if !outcome.Passed() {
		t.Fatalf("serial scenario failed: %v", outcome.Failures)
	}
}

type blockingPairDriver struct {
	entered chan struct{}
	release chan struct{}
}

func TestFilterByRuntimeRatioKeepsOnlyDeclaredPerformanceContracts(t *testing.T) {
	scenarios := []*Scenario{
		{Name: "bounded", Assert: AssertSpec{RuntimeRatioMax: 1.5}},
		{Name: "unbounded"},
	}
	got := FilterByRuntimeRatio(scenarios)
	if len(got) != 1 || got[0].Name != "bounded" {
		t.Fatalf("runtime-ratio filter = %v, want bounded only", got)
	}
}

func TestFilterByTagsRequiresEveryRequestedTag(t *testing.T) {
	hermeticFast := &Scenario{Name: "hermetic-fast", Tags: []string{"fast", "hermetic"}}
	fastAuth := &Scenario{Name: "fast-auth", Tags: []string{"fast", "requires-auth"}}
	hermetic := &Scenario{Name: "hermetic", Tags: []string{"hermetic"}}

	got := FilterByTags([]*Scenario{hermeticFast, fastAuth, hermetic}, []string{"fast", "hermetic"})
	if len(got) != 1 || got[0] != hermeticFast {
		t.Fatalf("filtered scenarios = %#v, want only fast+hermetic", got)
	}
}

func TestFilterByDriversAcceptsRequestedAlternatives(t *testing.T) {
	cli := &Scenario{Driver: "cli-mode"}
	rpc := &Scenario{Driver: "rpc-mode"}
	tmux := &Scenario{Driver: "interactive-tmux"}
	got := FilterByDrivers([]*Scenario{cli, rpc, tmux}, []string{"cli-mode", "rpc-mode"})
	if len(got) != 2 || got[0] != cli || got[1] != rpc {
		t.Fatalf("driver filter = %#v, want CLI and RPC", got)
	}
}

func TestDefaultSchedulerSerializesExtensionColdStarts(t *testing.T) {
	limits, err := parseGroupLimits(defaultParityGroupLimits)
	if err != nil {
		t.Fatal(err)
	}
	if limits["process-ext"] != 1 || limits["tmux-ext"] != 1 {
		t.Fatalf("extension limits = process:%d tmux:%d, want 1/1", limits["process-ext"], limits["tmux-ext"])
	}
}

func TestScenarioWithRunsDoesNotMutateDeclaration(t *testing.T) {
	scenario := &Scenario{Assert: AssertSpec{Runs: 3}}
	override := scenarioWithRuns(scenario, 1)
	if override.Assert.Runs != 1 {
		t.Fatalf("override runs = %d, want 1", override.Assert.Runs)
	}
	if scenario.Assert.Runs != 3 {
		t.Fatalf("declared runs mutated to %d", scenario.Assert.Runs)
	}
}

func (*blockingPairDriver) Name() string { return "blocking-pair" }
func (d *blockingPairDriver) Run(context.Context, *testing.T, BinaryRef, *Scenario) Result {
	d.entered <- struct{}{}
	<-d.release
	return Result{ReadyOK: true}
}

func TestRunScenarioRunsNonPerfPairConcurrently(t *testing.T) {
	driver := &blockingPairDriver{entered: make(chan struct{}, 2), release: make(chan struct{})}
	const name = "parallel-pair"
	DriverRegistry[name] = driver
	defer delete(DriverRegistry, name)
	scenario := &Scenario{Driver: name}
	done := make(chan *ScenarioOutcome, 1)
	go func() {
		done <- RunScenario(context.Background(), t, scenario, BinaryRef{Label: "pig"}, BinaryRef{Label: "pi"}, true)
	}()
	for range 2 {
		select {
		case <-driver.entered:
		case <-time.After(time.Second):
			t.Fatal("Pig/Pi pair did not start concurrently")
		}
	}
	close(driver.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parallel pair did not finish")
	}
}

type mismatchingDriver struct{ calls int }

func (*mismatchingDriver) Name() string { return "mismatching" }
func (d *mismatchingDriver) Run(_ context.Context, _ *testing.T, bin BinaryRef, _ *Scenario) Result {
	d.calls++
	return Result{ReadyOK: true, Output: bin.Label}
}

func TestRunScenarioStopsAfterFirstComparatorFailure(t *testing.T) {
	driver := &mismatchingDriver{}
	const name = "comparator-fail-fast"
	DriverRegistry[name] = driver
	defer delete(DriverRegistry, name)
	scenario := &Scenario{Driver: name}
	scenario.Assert.Runs = 3
	scenario.Assert.OutputEqual = true
	outcome := RunScenario(context.Background(), t, scenario, BinaryRef{Label: "pig"}, BinaryRef{Label: "pi"}, false)
	if driver.calls != 2 || len(outcome.Pig.Runs) != 1 || len(outcome.Pi.Runs) != 1 {
		t.Fatalf("calls/pig/pi = %d/%d/%d, want one mismatched pair only", driver.calls, len(outcome.Pig.Runs), len(outcome.Pi.Runs))
	}
	if len(outcome.Failures) == 0 {
		t.Fatal("comparator mismatch was not retained")
	}
}

func TestAC61RunScenarioStopsAfterFirstFailedPair(t *testing.T) {
	driver := &countingDriver{}
	const name = "counting-fail-fast"
	previous, existed := DriverRegistry[name]
	DriverRegistry[name] = driver
	defer func() {
		if existed {
			DriverRegistry[name] = previous
		} else {
			delete(DriverRegistry, name)
		}
	}()
	scenario := &Scenario{Driver: name}
	scenario.Assert.Runs = 3
	outcome := RunScenario(context.Background(), t, scenario, BinaryRef{Label: "pig"}, BinaryRef{Label: "pi"}, false)
	if driver.calls != 2 || len(outcome.Pig.Runs) != 1 || len(outcome.Pi.Runs) != 1 {
		t.Fatalf("calls/pig/pi = %d/%d/%d, want one failed pair only", driver.calls, len(outcome.Pig.Runs), len(outcome.Pi.Runs))
	}
}
