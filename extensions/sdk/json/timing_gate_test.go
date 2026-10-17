// SPDX-License-Identifier: MIT

//go:build !race

package json

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// timingGate is internal/testenv.TimingGate for this module, which cannot import the main module's internal packages: a test that fails on a measured timing calls it, and with PIG_PERF_TIMING_REPORT_ONLY=1 (pull-request CI) the failure is a GitHub Actions warning instead. testenv's TestTimingGatesAreListed keeps this copy's contract equal to the original.
func timingGate(t testing.TB, failed bool, format string, args ...any) {
	t.Helper()
	if !failed {
		return
	}
	message := fmt.Sprintf(format, args...)
	if os.Getenv("PIG_PERF_TIMING_REPORT_ONLY") != "1" {
		t.Error(message)
		return
	}
	t.Logf("timing gate (report only): %s", message)
	line := "::warning title=timing gate::" + strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(t.Name()+": "+message)
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
