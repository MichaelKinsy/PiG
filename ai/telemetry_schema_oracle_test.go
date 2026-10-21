package ai

// pi: packages/ai/src/telemetry.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestAITelemetrySchemaMatchesPi evaluates Pi's AI_TELEMETRY_SCHEMA declaration as written and compares the schema JSON member by
// member with AITelemetrySchema's: spans, events, attributes with their values, status, and metrics with kind, unit and attributes.
// Object member order is not compared; Go writes map members sorted and JSON objects are unordered.
func TestAITelemetrySchemaMatchesPi(t *testing.T) {
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "telemetry.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/telemetry_schema.mjs", source)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(AITelemetrySchema())
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AITelemetrySchema differs from Pi's AI_TELEMETRY_SCHEMA:\n got %s\nwant %s", encoded, output)
	}
}
