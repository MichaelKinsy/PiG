package extensionconformance

import (
	_ "embed"
	"encoding/json"
	"os"
	"testing"
)

// The probe extensions the extension_runner row loads (testdata/extension-runner.mjs is the Node script that defines the expectation): the runner dispatches to these two, in this order.
//
//go:embed testdata/extension-runner-probe-a.mjs
var extensionRunnerProbeA string

//go:embed testdata/extension-runner-probe-b.mjs
var extensionRunnerProbeB string

// sessionGraphExtensionRunnerWant is the report of Pi's own createExtensionRuntime and ExtensionRunner (the Node row).
//
//go:embed testdata/session-graph-extension-runner.json
var sessionGraphExtensionRunnerWant string

// createExtensionRuntime and ExtensionRunner (core/extensions/loader.ts, runner.ts) for every SDK, against the report of Pi's own classes (the Node row): a runner over two probe extensions a DefaultResourceLoader loaded answers the registries (tools, commands, flags, shortcuts, handlers, renderers), dispatches every emit through both extensions' handlers in order with Pi's chaining and error isolation, binds the actions and contexts an extension's own calls reach, wraps the UI context's dialogs with prompt events, and fails with its message once invalidated.
func TestSessionGraphExtensionRunnerAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphSDK(t, extensionRunnerRow)
}

// TestPackedSessionGraphExtensionRunnerAcrossSDKs is the extension_runner row in the packed cells of the Go, Rust and Python SDKs.
func TestPackedSessionGraphExtensionRunnerAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphPackedWith(t, newSessionGraphRig, extensionRunnerRow)
}

func extensionRunnerRow(t *testing.T, r *sessionGraphRig, _ string) {
	report, root := r.script("extension_runner")
	got := normalizeSessionGraph(t, report, root)
	if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
		encoded, _ := json.MarshalIndent(got, "", "\t")
		_ = os.WriteFile(path, encoded, 0o600)
	}
	requireSessionGraphReport(t, got, sessionGraphExtensionRunnerWant)
}
