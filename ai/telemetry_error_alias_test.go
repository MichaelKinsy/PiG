package ai

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// aliases reports whether part's bytes lie inside source's backing array.
func aliases(source, part string) bool {
	if part == "" {
		return false
	}
	s, p := uintptr(unsafe.Pointer(unsafe.StringData(source))), uintptr(unsafe.Pointer(unsafe.StringData(part))) //nolint:gosec // the test compares backing-array addresses
	return p >= s && p < s+uintptr(len(source))
}

// The provider-error span status carries Pi's ownedSlice(errorMessage, 0, 500) (telemetry.ts:195): the span must not retain
// the whole error message.
func TestRequestErrorSpanStatusDoesNotRetainTheErrorMessage(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	result := aiTelemetryMessage(StopReasonError)
	result.ErrorMessage = strings.Repeat("x", 1<<20)
	models := aiTelemetrySetup(t, result)
	model := aiTelemetryModel()
	aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) *AssistantMessage {
		return models.FetchDeferred(t.Context(), model, aiTelemetryHandle(model), DeferredFetchOptions{StreamOptions: StreamOptions{Telemetry: span}})
	})
	aiTelemetrySettled(t, recorder)
	status := recorder.GetSpans()[1].Status
	if status.Error == nil || len(status.Error.Message) != 500 {
		t.Fatalf("status = %+v, want a 500-unit message", status)
	}
	if aliases(result.ErrorMessage, status.Error.Message) {
		t.Error("the span status message aliases the full error message")
	}
}
