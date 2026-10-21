package harness

// pi: packages/durable/src/harness/telemetry.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestDurableTelemetrySchemaMatchesPi evaluates Pi's DURABLE_TELEMETRY_SCHEMA declaration as written and compares the schema JSON member by
// member with DurableTelemetrySchema's: spans, events, attributes with their values, status, and metrics with kind, unit and attributes.
// Object member order is not compared; Go writes map members sorted and JSON objects are unordered.
func TestDurableTelemetrySchemaMatchesPi(t *testing.T) {
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "durable", "src", "harness", "telemetry.ts")
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
	encoded, err := json.Marshal(DurableTelemetrySchema)
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DurableTelemetrySchema differs from Pi's DURABLE_TELEMETRY_SCHEMA:\n got %s\nwant %s", encoded, output)
	}
}
