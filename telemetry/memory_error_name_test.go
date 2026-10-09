package telemetry_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// namedTelemetryError is a Go port of a JavaScript Error subclass that assigns `name`.
type namedTelemetryError struct{ message string }

func (err *namedTelemetryError) Error() string { return err.message }
func (*namedTelemetryError) Name() string      { return "NamedError" }

// Pi packages/telemetry/src/memory.ts:78-86 automaticErrorStatus records `error.name` and `error.message` of a thrown Error,
// and no details for a thrown non-Error. Go's counterpart of `error.name` is the error's Name method, otherwise "Error"
// (ai/diagnostics.go errorName). Measured on Pi 1.1.0 InMemoryTelemetryContext, throwing
// NamedError("a"), an unnamed Error subclass("b"), TypeError("c"), the string "s" and NamedError(""):
//
//	[{"status":"error","error":{"name":"NamedError","message":"a"}},{"status":"error","error":{"name":"Error","message":"b"}},
//	 {"status":"error","error":{"name":"TypeError","message":"c"}},{"status":"error"},{"status":"error","error":{"name":"NamedError","message":""}}]
//
// A returned error and a panic are both Pi's rejection.
func TestInMemoryAutomaticErrorStatusUsesTheErrorNameAsPi(t *testing.T) {
	failures := []any{&namedTelemetryError{"a"}, errors.New("b"), "s", &namedTelemetryError{""}}
	want := `[{"status":"error","error":{"name":"NamedError","message":"a"}},{"status":"error","error":{"name":"Error","message":"b"}},{"status":"error"},{"status":"error","error":{"name":"NamedError","message":""}}]`
	for _, panics := range []bool{false, true} {
		recorder := telemetry.NewInMemoryTelemetryContext()
		for _, failure := range failures {
			err, isError := failure.(error)
			if !panics && !isError {
				// A Go callback can only return an error; a thrown non-Error is a panic.
				func() {
					defer func() { _ = recover() }()
					_ = recorder.StartSpan(telemetry.SpanOptions{Name: "x"}, func(telemetry.TelemetrySpan) error { panic(failure) })
				}()
				continue
			}
			func() {
				defer func() { _ = recover() }()
				_ = recorder.StartSpan(telemetry.SpanOptions{Name: "x"}, func(telemetry.TelemetrySpan) error {
					if panics {
						panic(failure)
					}
					return err
				})
			}()
		}
		type statusError struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}
		type status struct {
			Status telemetry.SpanStatusCode `json:"status"`
			Error  *statusError             `json:"error,omitempty"`
		}
		statuses := make([]status, 0, len(failures))
		for _, span := range recorder.GetSpans() {
			recorded := status{Status: span.Status.Status}
			if span.Status.Error != nil {
				recorded.Error = &statusError{span.Status.Error.Name, span.Status.Error.Message}
			}
			statuses = append(statuses, recorded)
		}
		got, err := json.Marshal(statuses)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("panics=%v statuses = %s\nwant Pi's %s", panics, got, want)
		}
	}
}
