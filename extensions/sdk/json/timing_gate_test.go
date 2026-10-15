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
	fmt.Fprintln(os.Stdout, line)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600); err == nil {
			fmt.Fprintf(file, "%s\n\n", line)
			_ = file.Close()
		}
	}
}
