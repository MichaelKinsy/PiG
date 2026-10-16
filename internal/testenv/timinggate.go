// SPDX-License-Identifier: MIT

package testenv

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TimingReportOnlyEnv names the environment variable that turns every [TimingGate] and [TimingGateFatal] into a report. Pull-request CI sets it to 1 on its shared runners; the dedicated release perf run (make test-timing) leaves it unset.
const TimingReportOnlyEnv = "PIG_PERF_TIMING_REPORT_ONLY"

// TimingGate is the one mechanism for a test that fails on a measured duration, a CPU time, an ns/op or a ratio of two timings compared to a bar. Pass the comparison's outcome as failed and the failure message as format and args; the call fails the test with t.Errorf semantics when failed is true, and the test goes on.
//
// A timing depends on the machine's speed and load, so the owner's ruling (2026-10-15) splits the performance gates: a gate on a count (allocations, bytes, binary size, descriptors, goroutines, processes, temp entries) stays blocking everywhere and never calls this function, while a gate on a timing is blocking only on the quiet pinned hardware of the release perf run and report-only in pull-request CI. When [TimingReportOnlyEnv] is 1, a true failed does not fail the test: it logs the message and prints the GitHub Actions workflow command "::warning title=timing gate::<test name>: <message>" on stdout, and appends the same line to the file named by GITHUB_STEP_SUMMARY when that is set, because go test hides the stdout of a passing package without -v. The test still measures and still checks; only the verdict differs. With the variable unset or any other value the gate is blocking.
//
// Every test that calls TimingGate or [TimingGateFatal] is listed in automation/perf/timing-gates.txt, which make test-timing runs with the variable unset; TestTimingGatesAreListed keeps that list complete.
func TimingGate(t testing.TB, failed bool, format string, args ...any) {
	t.Helper()
	timingGate(t, false, failed, format, args)
}

// TimingGateFatal is [TimingGate] with t.Fatalf semantics: when the gate blocks, a true failed stops the test at once. Report-only, it returns and the test goes on, so code after the call must not rely on the bar having held.
func TimingGateFatal(t testing.TB, failed bool, format string, args ...any) {
	t.Helper()
	timingGate(t, true, failed, format, args)
}

func timingGate(t testing.TB, fatal, failed bool, format string, args []any) {
	t.Helper()
	if !failed {
		return
	}
	message := fmt.Sprintf(format, args...)
	if os.Getenv(TimingReportOnlyEnv) != "1" {
		if fatal {
			t.Fatal(message)
		}
		t.Error(message)
		return
	}
	t.Logf("timing gate (report only): %s", message)
	line := "::warning title=timing gate::" + escapeWorkflowCommand(t.Name()+": "+message)
	// In report-only mode the annotation is the gate's only output, so a failed write fails the test instead of losing the miss.
	if _, err := fmt.Fprintln(os.Stdout, line); err != nil {
		t.Error("timing gate: write annotation: " + err.Error())
	}
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendStepSummary(path, line); err != nil {
			t.Error("timing gate: write step summary: " + err.Error())
		}
	}
}

// appendStepSummary appends line as its own paragraph to the GitHub step summary file at path.
func appendStepSummary(path, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%s\n\n", line); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// escapeWorkflowCommand encodes the characters a workflow command's message cannot carry.
func escapeWorkflowCommand(message string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(message)
}
