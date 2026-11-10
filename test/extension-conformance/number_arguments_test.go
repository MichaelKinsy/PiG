package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestConformance_NumberAndKeyArgumentsAgreeAcrossSDKs: Pi reads a tool's arguments with JSON.parse, which takes a number outside the double range (1e400)
// as Infinity (written back as null), rounds an integer past 2^53 to the nearest double, keeps the last of a duplicate key and accepts __proto__ and
// the empty string as keys. The Node SDK runs Pi's reader, so its echo of the arguments is the reference. The Go SDK refused 1e400 and failed the call, and
// the Rust SDK treated the refused frame as a broken connection, so one such argument ended the extension; each case ends with a call that proves it still serves.
func TestConformance_NumberAndKeyArgumentsAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	inputs := []string{
		`{"v":1e400}`, `{"v":-1e400}`, `{"v":[1e999,1e-400,-1E+309,2.5]}`, `{"v":"1e400"}`,
		`{"v":9007199254740993}`, `{"v":-9007199254740993}`, `{"v":18446744073709551615}`, `{"v":18446744073709551616}`, `{"v":123456789012345678901234567890}`,
		`{"v":-0}`, `{"v":-0.0}`, `{"v":1E5}`, `{"v":1.0}`, `{"v":0.30000000000000004}`, `{"v":5e-324}`, `{"v":1e21}`, `{"v":100000000000000000000}`, `{"v":0.000001}`, `{"v":1e-7}`,
		`{"v":4.35}`, `{"v":[1.10,2.00,3e0]}`, `{"v":12345678901234567890.123456789}`, `{"v":123456789012345680000}`,
		`{"a":1,"a":2}`, `{"__proto__":1,"b":2}`, `{"":1}`, `{"v":"\u00e9\ud83d\ude00"}`, `{"v":"\u0000\u001f\u007f\u2028\u2029"}`,
	}
	type outcome struct {
		details string
		failed  bool
	}
	// runAll echoes every input through the extension. An error from the reference ends the test: the inputs are all valid JSON, so Pi reads each of them, and an
	// error is the reference process failing, not an outcome to compare a candidate with.
	runAll := func(t *testing.T, h *harness, reference bool) []outcome {
		t.Helper()
		tool, ok := findTool(h.runner, "echo_params")
		if !ok {
			t.Fatal("echo_params not registered")
		}
		call := func(args string) outcome {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := tool.Definition.Execute(ctx, "tc-number", json.RawMessage(args), nil)
			if err != nil {
				if reference {
					t.Fatalf("the Node (Pi) reference failed %s: %v", args, err)
				}
				t.Logf("%s: %v", args, err)
				return outcome{failed: true}
			}
			encoded, err := json.Marshal(result.Details)
			if err != nil {
				t.Fatal(err)
			}
			return outcome{details: string(encoded)}
		}
		var outcomes []outcome
		for _, args := range inputs {
			outcomes = append(outcomes, call(args))
			if after := call(`{"text":"after"}`); after.failed || after.details != `{"text":"after"}` {
				t.Fatalf("the call after %s: %+v; the extension stopped serving", args, after)
			}
		}
		return outcomes
	}
	shutdown := func(h *harness) {
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	}

	var nodeCase harnessCase
	for _, tc := range sdkHarnessCases() {
		if tc.name == "subprocess-node" {
			nodeCase = tc
		}
	}
	if nodeCase.make == nil {
		t.Fatal("sdkHarnessCases has no subprocess-node case, so there is no Node (Pi) reference to compare with")
	}
	// The reference row records the reference, and so does the first candidate row when -run leaves the reference row out.
	reference := referenceRow[[]outcome]{name: "Node (Pi)"}
	recordReference := func(t *testing.T) []outcome {
		t.Helper()
		h := nodeCase.make(t)
		defer shutdown(h)
		got := runAll(t, h, true)
		// Values the JSON.parse rules imply, so a mistake shared by every SDK cannot pass.
		for input, want := range map[string]string{`{"v":1e400}`: `{"v":null}`, `{"a":1,"a":2}`: `{"a":2}`, `{"v":9007199254740993}`: `{"v":9007199254740992}`, `{"v":-0}`: `{"v":0}`, `{"":1}`: `{"":1}`} {
			for i, in := range inputs {
				if in == input && (got[i].failed || got[i].details != want) {
					t.Fatalf("reference echo of %s = %+v, want %s", in, got[i], want)
				}
			}
		}
		return got
	}
	t.Run(nodeCase.name, func(t *testing.T) {
		reference.set(t, recordReference(t))
	})

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if tc.name == "subprocess-node" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			want := reference.get(t, func() []outcome { return recordReference(t) })
			h := tc.make(t)
			t.Cleanup(func() { shutdown(h) })
			for i, got := range runAll(t, h, false) {
				if !reflect.DeepEqual(got, want[i]) {
					t.Errorf("echo of %s = %+v, Node (Pi) reports %+v", inputs[i], got, want[i])
				}
			}
		})
	}
}
