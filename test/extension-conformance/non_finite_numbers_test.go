package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestConformance_NonFiniteNumbersInToolResultsAcrossSDKs: JSON.stringify writes NaN and Infinity as null, and the Node and Rust SDKs do the same. Python's json module writes them as the bare words NaN and Infinity, which is
// not JSON: the host refused the frame and the extension's connection ended, so the next call failed too. The Go SDK's codec refused them as encoding/json does, failing the call; it now writes null too (json.MarshalJS).
// Every SDK keeps serving afterwards.
func TestConformance_NonFiniteNumbersInToolResultsAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			nan, ok := findTool(h.runner, "nan_out")
			if !ok {
				t.Fatal("nan_out not registered")
			}
			echo, ok := findTool(h.runner, "echo")
			if !ok {
				t.Fatal("echo not registered")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := nan.Definition.Execute(ctx, "tc-nan", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatalf("a tool returning NaN and Infinity: %v", err)
			}
			encoded, _ := json.Marshal(result.Details)
			var got, want any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal([]byte(`{"n":null,"i":null,"l":[null,2,{"d":null}],"ok":1}`), &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("details = %s, want every non-finite number, in objects and arrays, as null", encoded)
			}
			after, err := echo.Definition.Execute(ctx, "tc-after", json.RawMessage(`{"text":"after"}`), nil)
			if err != nil || !strings.Contains(after.Text(), "after") {
				t.Fatalf("the call after the NaN result: %q, %v; the extension stopped serving", after.Text(), err)
			}
		})
	}
}
