package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestConformance_DeeplyNestedToolArgumentsAgreeAcrossSDKs: Pi's arguments are parsed by JSON.parse, which takes thousands of levels, and the Go, Node and Python SDKs do the same. serde_json refuses past 128 levels, and
// the Rust SDK treated the refused frame as a broken connection, so a tool argument or event payload nested 130 deep ended the extension (the host saw "read failed: EOF"). echo_params returns its
// arguments as its details: each depth must come back intact, and the call after the deepest one proves the extension is still serving.
func TestConformance_DeeplyNestedToolArgumentsAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	nested := func(depth int) string {
		return `{"v":` + strings.Repeat("[", depth) + `{"leaf":1}` + strings.Repeat("]", depth) + `}`
	}
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
			tool, ok := findTool(h.runner, "echo_params")
			if !ok {
				t.Fatal("echo_params not registered")
			}
			run := func(args string) (string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				result, err := tool.Definition.Execute(ctx, "tc-deep", json.RawMessage(args), nil)
				if err != nil {
					return "", err
				}
				encoded, err := json.Marshal(result.Details)
				return string(encoded), err
			}
			for _, depth := range []int{100, 130, 1000} {
				want := nested(depth)
				if got, err := run(want); err != nil || got != want {
					t.Fatalf("depth %d: details %.80s... (%d bytes), error %v; want the arguments back unchanged", depth, got, len(got), err)
				}
			}
			if got, err := run(`{"text":"after"}`); err != nil || got != `{"text":"after"}` {
				t.Fatalf("the call after the deepest one: %q, %v; the extension stopped serving", got, err)
			}
		})
	}
}
