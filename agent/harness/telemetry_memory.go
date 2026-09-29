package harness

import "github.com/MichaelKinsy/PiG/telemetry"

// InMemoryTelemetryContext records backend-neutral spans in process memory.
type InMemoryTelemetryContext = telemetry.InMemoryTelemetryContext

// RecordedTelemetrySpan is a detached span snapshot.
type RecordedTelemetrySpan = telemetry.RecordedTelemetrySpan

// RecordedTelemetryEvent is a detached span-event snapshot.
type RecordedTelemetryEvent = telemetry.RecordedTelemetryEvent
