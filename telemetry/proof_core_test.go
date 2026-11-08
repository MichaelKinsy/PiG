package telemetry_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// A TelemetrySchemaDefinition keeps its Metrics through DefineTelemetrySchema and a JSON round trip (Pi's schema is plain data whose
// `metrics` member survives serialization); an absent Metrics stays absent and an empty one stays {}.
func TestTelemetrySchemaDefinitionMetricsSurviveDefineAndARoundTrip(t *testing.T) {
	metric := telemetry.TelemetryMetricDefinition{
		Description: "requests", Unit: "1", Kind: telemetry.TelemetryMetricKind("counter"),
		Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{},
	}
	schema := &telemetry.TelemetrySchemaDefinition{Version: 1, Spans: map[string]telemetry.TelemetrySpanDefinition{}, Metrics: map[string]telemetry.TelemetryMetricDefinition{"requests": metric}}
	if got := telemetry.DefineTelemetrySchema(schema); got != schema || len(got.Metrics) != 1 {
		t.Fatalf("DefineTelemetrySchema returned %p with %d metrics, want the same schema with 1", got, len(got.Metrics))
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var back telemetry.TelemetrySchemaDefinition
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Metrics["requests"]; got.Description != "requests" || got.Unit != "1" || got.Kind != metric.Kind {
		t.Errorf("Metrics[requests] after a round trip = %+v, want %+v", got, metric)
	}
	absent, _ := json.Marshal(&telemetry.TelemetrySchemaDefinition{Version: 1, Spans: map[string]telemetry.TelemetrySpanDefinition{}})
	if strings.Contains(string(absent), "metrics") {
		t.Errorf("a nil Metrics serialized as %s, want no metrics member", absent)
	}
	empty, _ := json.Marshal(&telemetry.TelemetrySchemaDefinition{Version: 1, Spans: map[string]telemetry.TelemetrySpanDefinition{}, Metrics: map[string]telemetry.TelemetryMetricDefinition{}})
	if !strings.Contains(string(empty), `"metrics":{}`) {
		t.Errorf("an empty Metrics serialized as %s, want \"metrics\":{}", empty)
	}
}
