package harness

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// sharesBacking reports whether part's bytes lie inside source's backing array.
func sharesBacking(source, part string) bool {
	if part == "" {
		return false
	}
	s, p := uintptr(unsafe.Pointer(unsafe.StringData(source))), uintptr(unsafe.Pointer(unsafe.StringData(part)))
	return p >= s && p < s+uintptr(len(source))
}

// Pi durable/src/harness/tool.ts:1051 keeps `ownedSlice(text, 0, 500)` of a nested call's error text: the summary a live view keeps
// must not pin the whole text.
func TestNestedSummaryErrorDoesNotRetainTheErrorText(t *testing.T) {
	text := strings.Repeat("e", 1<<20)
	result := durable.NestedToolExecutionResult{IsError: true, Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityError, Message: text}}}
	summary := summaryOf(result, nil)
	if len(summary.Error) != maxSummaryErrorChars {
		t.Fatalf("summary error has %d units, want %d", len(summary.Error), maxSummaryErrorChars)
	}
	if sharesBacking(text, summary.Error) {
		t.Error("the summary's error text aliases the full error text")
	}
}

// Pi durable/src/harness/scheduler.ts:2269 sets the task span status to `ownedSlice(message, 0, 500)`: the span must not pin the
// whole failure message.
func TestTaskSpanStatusDoesNotRetainTheFailureMessage(t *testing.T) {
	message := strings.Repeat("f", 1<<20)
	recorder := telemetry.NewInMemoryTelemetryContext()
	scheduler := &TaskScheduler{}
	fault := &anyOutcome{Status: durable.OutcomeFaulted, Error: &durable.TaskOutcomeError{Message: message}}
	err := recorder.StartSpan(telemetry.SpanOptions{Name: "task"}, func(span telemetry.TelemetrySpan) error {
		scheduler.finishPhase(&invocation{fault: fault}, span, anyTaskRecord{}, "run", 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status := recorder.GetSpans()[0].Status
	if status.Error == nil || len(status.Error.Message) != 500 {
		t.Fatalf("status = %+v, want a 500-unit message", status)
	}
	if sharesBacking(message, status.Error.Message) {
		t.Error("the span status message aliases the full failure message")
	}
}
