package telemetry

import (
	"encoding/json"
	"testing"
)

// upstream: packages/telemetry/src/index.ts:27-32 (TelemetryAttributeMetadata): description is required, sensitive and cardinality are optional and absent from the schema JSON when unset; cardinality is "low" or "high".
func TestTelemetryAttributeMetadataCardinalityJSON(t *testing.T) {
	sensitive := true
	for _, c := range []struct {
		name string
		meta TelemetryAttributeMetadata
		want string
	}{
		{"unset", TelemetryAttributeMetadata{Description: "d"}, `{"description":"d"}`},
		{"low", TelemetryAttributeMetadata{Description: "d", Cardinality: TelemetryAttributeCardinalityLow}, `{"description":"d","cardinality":"low"}`},
		{"high and sensitive", TelemetryAttributeMetadata{Description: "d", Sensitive: &sensitive, Cardinality: TelemetryAttributeCardinalityHigh}, `{"description":"d","sensitive":true,"cardinality":"high"}`},
	} {
		encoded, err := json.Marshal(c.meta)
		if err != nil || string(encoded) != c.want {
			t.Errorf("%s: %s, %v; want %s", c.name, encoded, err, c.want)
		}
		var decoded TelemetryAttributeMetadata
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Cardinality != c.meta.Cardinality {
			t.Errorf("%s: decoded cardinality %q, %v", c.name, decoded.Cardinality, err)
		}
	}
	// The definition embeds the metadata, so its attribute JSON carries the cardinality beside type.
	encoded, err := json.Marshal(TelemetryAttributeDefinition{TelemetryAttributeMetadata: TelemetryAttributeMetadata{Description: "d", Cardinality: TelemetryAttributeCardinalityLow}, Type: TelemetryAttributeTypeString})
	if err != nil || string(encoded) != `{"description":"d","cardinality":"low","type":"string"}` {
		t.Errorf("definition = %s, %v", encoded, err)
	}
}
