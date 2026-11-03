package telemetry_test

// Field survival of TelemetrySchemaDefinition: its Metrics (like its Version and Spans) are the same after DefineTelemetrySchema,
// after the JSON round trip of the serializable schema data, and in the instruments CreateTypedMetrics builds from them.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

func TestTelemetrySchemaDefinitionRegistrationKeepsMetricsThroughDefineAndJSONRoundTrip(t *testing.T) {
	schema := trafficSchema()
	schema.Metrics["network.latency"] = telemetry.TelemetryMetricDefinition{
		Kind: telemetry.TelemetryMetricKindHistogram, Description: "Round trip time", Unit: "ms",
		Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{}, Boundaries: []float64{1, 10, 100},
	}
	defined := telemetry.DefineTelemetrySchema(schema)
	if defined != schema {
		t.Fatal("DefineTelemetrySchema returned another schema")
	}
	if len(defined.Metrics) != 3 || defined.Version != 1 {
		t.Fatalf("defined schema = %+v", defined)
	}

	encoded, err := json.Marshal(defined)
	if err != nil {
		t.Fatal(err)
	}
	var decoded telemetry.TelemetrySchemaDefinition
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("%s: %v", encoded, err)
	}
	if !reflect.DeepEqual(decoded.Metrics, defined.Metrics) {
		t.Fatalf("Metrics changed in the round trip:\n got %+v\nwant %+v", decoded.Metrics, defined.Metrics)
	}
	if decoded.Version != defined.Version || len(decoded.Spans) != 0 {
		t.Fatalf("decoded = %+v", decoded)
	}

	// The decoded metrics are what instruments read: description, unit and boundaries reach the measurement.
	adapter := telemetry.NewInMemoryTelemetryContext()
	typed := telemetry.CreateTypedMetrics(adapter, &decoded)
	typed.Counter("network.bytes").Add(5, nil)
	measurements := adapter.GetMetrics()
	if len(measurements) != 1 || measurements[0].Description == nil || *measurements[0].Description != "Encoded bytes" ||
		measurements[0].Unit == nil || *measurements[0].Unit != "By" {
		t.Fatalf("measurements = %+v, want the schema's description and unit", measurements)
	}
}
