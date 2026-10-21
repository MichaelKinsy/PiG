// Supports the ports of packages/durable/test/harness-telemetry.test.ts.

package harness

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// openTracked is harness-telemetry.test.ts openTracked: a Harness reporting to the recorder, with the faux root.
func openTracked(t *testing.T, store durable.Storage, setup *chatState, recorder *telemetry.InMemoryTelemetryContext) (Harness, Conversation) {
	t.Helper()
	return openChat(t, store, setup, openChatOptions{telemetry: recorder})
}

// toolReply is harness-telemetry.test.ts toolReply: a tool-calling faux answer for call-1.
func toolReply(name string, args map[string]any) ai.FauxResponseStep {
	if args == nil {
		args = map[string]any{}
	}
	return tlCallsStep(tlCall{name: name, args: args, id: "call-1"})
}

// telemetrySpans are the recorded spans named name.
func telemetrySpans(recorder *telemetry.InMemoryTelemetryContext, name string) []telemetry.RecordedTelemetrySpan {
	var spans []telemetry.RecordedTelemetrySpan
	for _, span := range recorder.GetSpans() {
		if span.Name == name {
			spans = append(spans, span)
		}
	}
	return spans
}

// allSettled is `telemetry.getSpans().every((span) => span.settled)`.
func allSettled(recorder *telemetry.InMemoryTelemetryContext) bool {
	for _, span := range recorder.GetSpans() {
		if !span.Settled {
			return false
		}
	}
	return true
}

// measurements are the recorded measurements named name.
func measurements(recorder *telemetry.InMemoryTelemetryContext, name string) []telemetry.RecordedTelemetryMeasurement {
	var found []telemetry.RecordedTelemetryMeasurement
	for _, measurement := range recorder.GetMetrics() {
		if measurement.Name == name {
			found = append(found, measurement)
		}
	}
	return found
}

// spanEvents are the events of every span, in span order.
func spanEvents(spans []telemetry.RecordedTelemetrySpan) []telemetry.RecordedTelemetryEvent {
	var events []telemetry.RecordedTelemetryEvent
	for _, span := range spans {
		events = append(events, span.Events...)
	}
	return events
}

func hasEvent(events []telemetry.RecordedTelemetryEvent, name string, attributes telemetry.SpanAttributes) bool {
	for _, event := range events {
		if event.Name == name && reflect.DeepEqual(event.Attributes, attributes) {
			return true
		}
	}
	return false
}

// recordedText is JSON.stringify of the spans' attributes and events, for the content-free assertions.
func recordedText(t *testing.T, spans []telemetry.RecordedTelemetrySpan) string {
	t.Helper()
	type content struct {
		Attributes telemetry.SpanAttributes
		Events     []telemetry.RecordedTelemetryEvent
	}
	var all []content
	for _, span := range spans {
		all = append(all, content{span.Attributes, span.Events})
	}
	return jsonText(t, all)
}

// telemetryTaskState is the checkpoint of the tasks these tests define.
type telemetryTaskState struct {
	Phase string `json:"phase"`
	Count int    `json:"count,omitempty"`
}

type telemetryPhase = durable.PhaseHandler[durable.JsonValue, telemetryTaskState, durable.JsonValue, any]
type telemetryRuntime = durable.TaskRuntime[durable.JsonValue, telemetryTaskState, durable.JsonValue, any]
type telemetryRunning = durable.RunningTask[durable.JsonValue, telemetryTaskState, durable.JsonValue]

// finishPhase commits the completed outcome.
func finishPhase(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
	return runtime.Commit(ctx, func(durable.Tx, telemetryRunning) (*durable.NextTaskState[telemetryTaskState, durable.JsonValue], error) {
		return completed[telemetryTaskState, durable.JsonValue](nil), nil
	})
}

// abortHandler commits the aborted outcome.
func abortHandler(ctx context.Context, _ telemetryRunning, runtime telemetryRuntime) error {
	return runtime.Commit(ctx, func(durable.Tx, telemetryRunning) (*durable.NextTaskState[telemetryTaskState, durable.JsonValue], error) {
		return abortedWith[telemetryTaskState, durable.JsonValue](""), nil
	})
}

// telemetryTask defines a task of these tests that starts at initial.
func telemetryTask(name, initial string, phases map[string]telemetryPhase, abort ...durable.PhaseHandler[durable.JsonValue, telemetryTaskState, durable.JsonValue, any]) durable.Task[durable.JsonValue, telemetryTaskState, durable.JsonValue, any] {
	handler := durable.PhaseHandler[durable.JsonValue, telemetryTaskState, durable.JsonValue, any](abortHandler)
	if len(abort) > 0 {
		handler = abort[0]
	}
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, telemetryTaskState, durable.JsonValue, any]{
		Name:    name,
		Version: 1,
		Initial: func(durable.JsonValue) telemetryTaskState { return telemetryTaskState{Phase: initial} },
		Phases:  phases,
		Abort:   handler,
	})
}

// numberOf is a recorded span or event number attribute.
func numberOf[T ~int | ~int64 | ~uint64](id T) float64 { return float64(id) }

// attributeNames are the names of a recorded attribute bag in recorded order: Pi's object literal order, which the bag keeps
// end to end (start attributes, then attributes set later in the order they were first set).
func attributeNames(attributes telemetry.SpanAttributes) []string {
	names := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		names = append(names, attribute.Name)
	}
	return names
}
