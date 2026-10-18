package telemetry_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/telemetry/src/memory.ts

// Pi and PiG exchange carriers in both directions: PiG's inject output continues a span in Pi's recorder under the injecting span's context, and Pi's carrier continues a PiG span
// under it, so the carrier is the wire contract between the two implementations and not only within one.
func TestInMemoryCarriersInteroperateWithPi(t *testing.T) {
	pig := telemetry.NewInMemoryTelemetryContext()
	var carriers []telemetry.TelemetryCarrier
	for _, name := range []string{"first", "second"} {
		if err := pig.StartSpan(telemetry.SpanOptions{Name: name}, func(span telemetry.TelemetrySpan) error {
			carriers = append(carriers, pig.Inject(span))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	input, err := json.Marshal(carriers)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/propagation_oracle.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi struct {
		PiCarrier telemetry.TelemetryCarrier `json:"piCarrier"`
		Spans     []struct {
			Name          string                     `json:"name"`
			ParentID      *int                       `json:"parentId"`
			ParentContext telemetry.TelemetryCarrier `json:"parentContext"`
		} `json:"spans"`
	}
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatal(err)
	}
	for index, carrier := range carriers {
		name := fmt.Sprintf("from-pig-%d", index)
		found := false
		for _, span := range pi.Spans {
			if span.Name == name {
				found = true
				if span.ParentID != nil || !reflect.DeepEqual(span.ParentContext, carrier) {
					t.Errorf("Pi span %s has parent %v and parent context %v, want PiG's carrier %v as a remote parent", name, span.ParentID, span.ParentContext, carrier)
				}
			}
		}
		if !found {
			t.Errorf("Pi recorded no span %s", name)
		}
	}
	if len(pi.PiCarrier) == 0 {
		t.Fatal("Pi injected no carrier")
	}
	if err := pig.Extract(pi.PiCarrier).StartSpan(telemetry.SpanOptions{Name: "from-pi"}, func(telemetry.TelemetrySpan) error { return nil }); err != nil {
		t.Fatal(err)
	}
	spans := pig.GetSpans()
	continued := spans[len(spans)-1]
	if continued.Name != "from-pi" || continued.ParentID != nil || !reflect.DeepEqual(continued.ParentContext, pi.PiCarrier) {
		t.Fatalf("PiG span %+v, want Pi's carrier %v as a remote parent", continued, pi.PiCarrier)
	}
}
