package telemetry_test

// Ports packages/telemetry/test/propagation-worker.ts.
//
// Upstream's worker is a separate Node process: a fresh recorder (a fresh random scope and no shared registry) that
// extracts the carrier passed as its argument, records one "remote" span under it and prints the recorded spans as JSON.
// The Go form is the same boundary, not an in-process carrier round trip: the test binary re-executes itself as the
// worker, so the carrier crosses a real process boundary as an argument and the spans come back as JSON on stdout. An
// in-process round trip would not prove the absence of shared state (a package variable would survive it), which is what
// upstream's test is for. The helper-process re-execution is the idiom os/exec's own tests use; TestMain routes the
// re-executed binary to the worker before any test runs, so the worker is not a test and never appears in test output.

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// propagationWorkerEnv marks a re-executed test binary as the propagation worker.
const propagationWorkerEnv = "PIG_TELEMETRY_PROPAGATION_WORKER"

// recordedSpanJSON is the JSON of one span of getSpans(), with upstream's member names.
type recordedSpanJSON struct {
	ID            int                        `json:"id"`
	ParentID      *int                       `json:"parentId"`
	Context       telemetry.TelemetryCarrier `json:"context"`
	ParentContext telemetry.TelemetryCarrier `json:"parentContext"`
	Name          string                     `json:"name"`
}

func TestMain(m *testing.M) {
	if os.Getenv(propagationWorkerEnv) == "1" {
		os.Exit(runPropagationWorker(os.Args[1:], os.Stdout))
	}
	os.Exit(m.Run())
}

// runPropagationWorker is propagation-worker.ts: arguments[0] is the JSON carrier ("{}" when absent).
func runPropagationWorker(arguments []string, stdout *os.File) int {
	encoded := "{}"
	if len(arguments) > 0 {
		encoded = arguments[0]
	}
	var carrier telemetry.TelemetryCarrier
	if err := json.Unmarshal([]byte(encoded), &carrier); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	recorder := telemetry.NewInMemoryTelemetryContext()
	if err := recorder.Extract(carrier).StartSpan(telemetry.SpanOptions{Name: "remote"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var spans []recordedSpanJSON
	for _, span := range recorder.GetSpans() {
		spans = append(spans, recordedSpanJSON{ID: span.ID, ParentID: span.ParentID, Context: span.Context, ParentContext: span.ParentContext, Name: span.Name})
	}
	if err := json.NewEncoder(stdout).Encode(spans); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
