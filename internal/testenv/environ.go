package testenv

import (
	"encoding/json"
	"os"
)

// EnvironHelper is the environment variable that makes a test binary whose
// TestMain calls ReportEnvironIfRequested report its environment.
const EnvironHelper = "PIG_TEST_ENVIRON_HELPER"

// ReportEnvironIfRequested writes os.Environ, in the order of the process's
// environment block, to stdout as a JSON array and exits when EnvironHelper is
// "1". A TestMain calls it first, so the test binary can stand in for a
// spawned program.
func ReportEnvironIfRequested() {
	if os.Getenv(EnvironHelper) != "1" {
		return
	}
	if err := json.NewEncoder(os.Stdout).Encode(os.Environ()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
