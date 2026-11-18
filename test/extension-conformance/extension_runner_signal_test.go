package extensionconformance

import (
	"encoding/json"
	"os"
	"testing"
)

// The probe extension of the extension_runner_signal row (testdata/extension-runner-signal.mjs is the Node script that defines the expectation).
var extensionRunnerSignalProbe = testdataString("extension-runner-signal-probe.mjs")

// sessionGraphExtensionRunnerSignalWant is the report of Pi's own ExtensionRunner over real AbortControllers (the Node row).
var sessionGraphExtensionRunnerSignalWant = testdataString("session-graph-extension-runner-signal.json")

// A session-graph client's runs, each with its own signal (Pi: agent.ts:518-527, one AbortController per run), reach the extension as `ctx.signal`, read at access time (runner.ts:940-943). Two sequences, each against the report of Pi's own runner (the Node row): run 1 ends live and run 2 begins with no request between them, and run 2's abort must leave the signal a handler kept from run 1 alone (D235); run 1 is aborted and run 2 begins with no request between them, and the kept signal is the aborted one while the current one is not.
func TestSessionGraphRunSignalAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, runSignalRow)
}

// TestPackedSessionGraphRunSignalAcrossSDKs is the run-signal row in the packed cells of the Go, Rust, Python and Lisp SDKs.
func TestPackedSessionGraphRunSignalAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphPackedWith(t, newSessionGraphRig, runSignalRow)
}

func runSignalRow(t *testing.T, r *sessionGraphRig, _ string) {
	report, root := r.script("extension_runner_signal")
	got := normalizeSessionGraph(t, report, root)
	if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
		encoded, _ := json.MarshalIndent(got, "", "\t")
		_ = os.WriteFile(path, encoded, 0o600)
	}
	requireSessionGraphReport(t, got, sessionGraphExtensionRunnerSignalWant)
}
